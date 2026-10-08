package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/andyphied/otel-policy-lab/internal/telemetry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

// Diagnostics is the safe provenance of a real execution. Durations use whole
// milliseconds; exit_code is absent until a process has actually exited.
type Diagnostics struct {
	Binary          string                `json:"binary"`
	Version         string                `json:"version,omitempty"`
	BinarySHA256    string                `json:"binary_sha256,omitempty"`
	HarnessSHA256   string                `json:"harness_sha256,omitempty"`
	Pipelines       []PipelineDiagnostics `json:"pipelines"`
	Timings         map[string]int64      `json:"timings_ms"`
	StartTimeoutMS  int64                 `json:"start_timeout_ms"`
	RunTimeoutMS    int64                 `json:"run_timeout_ms"`
	SettleTimeMS    int64                 `json:"settle_time_ms"`
	ExitStatus      string                `json:"exit_status"`
	ExitCode        *int                  `json:"exit_code,omitempty"`
	FailureCategory string                `json:"failure_category,omitempty"`
}

// CollectorRunner executes processor chains in a supplied, trusted Collector.
// Replacing source exporters does not sandbox processors or the executable.
type CollectorRunner struct{ options Options }

func (r *CollectorRunner) Run(ctx context.Context, req RunRequest) (result *RunResult, runErr error) {
	started := time.Now()
	d := &Diagnostics{Binary: r.options.Binary, Pipelines: []PipelineDiagnostics{}, Timings: map[string]int64{}, ExitStatus: "not_started", StartTimeoutMS: r.options.StartTimeout.Milliseconds(), RunTimeoutMS: r.options.RunTimeout.Milliseconds(), SettleTimeMS: r.options.SettleTime.Milliseconds()}
	result = &RunResult{RunnerName: "otelcol", Input: &telemetry.Set{}, Output: &telemetry.Set{}, Diagnostics: d}
	fail := func(category, message string) (*RunResult, error) {
		d.FailureCategory = category
		return result, fmt.Errorf("Collector runner: %s", message)
	}
	runCtx, cancel := context.WithTimeout(ctx, r.options.RunTimeout)
	defer cancel()
	defer func() { d.Timings["total"] = time.Since(started).Milliseconds() }()
	if !processSupported() {
		return fail("unsupported_platform", "process-group lifecycle support requires macOS or Linux")
	}
	if runCtx.Err() != nil {
		return fail(contextCategory(runCtx), "overall deadline or cancellation interrupted the run before startup")
	}
	if r.options.Binary == "" {
		return fail("binary_required", "an explicit Collector binary is required")
	}
	binary, err := exec.LookPath(r.options.Binary)
	if err != nil {
		return fail("binary_unavailable", "the supplied Collector executable was not found or is not executable")
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return fail("binary_unavailable", "cannot resolve the supplied Collector executable")
	}
	d.Binary = binary
	configPath, err := filepath.Abs(req.CollectorConfigPath)
	if err != nil || req.CollectorConfigPath == "" {
		return fail("config_input", "a Collector configuration path is required")
	}
	raw := req.Raw
	if raw == nil {
		if req.FixturePath == "" {
			return fail("fixture_input", "full-fidelity OTLP fixture data or a fixture path is required")
		}
		raw, err = telemetry.LoadOTLP(req.FixturePath)
		if err != nil {
			return fail("fixture_input", "cannot load the OTLP fixture")
		}
	}
	if err := validateFixtureSize(raw); err != nil {
		return fail("fixture_limit", err.Error())
	}
	result.Input = raw.Normalize()
	telemetry.Canonicalize(result.Input)
	config, err := readLimited(configPath, maxConfigBytes)
	if err != nil {
		return fail("config_input", "cannot read the Collector configuration within the 4 MiB limit")
	}
	topology, err := parseTopology(config, raw)
	if err != nil {
		return fail("unsupported_topology", err.Error())
	}
	d.Pipelines = topology.pipelines
	result.InconclusiveReasons = topology.inconclusive(r.options.SettleTime)
	result.InconclusiveSignals = topology.inconclusiveSignals(r.options.SettleTime)
	d.BinarySHA256, err = binaryChecksum(runCtx, binary)
	if err != nil {
		if runCtx.Err() != nil {
			return fail("run_timeout", "overall deadline expired while identifying the binary")
		}
		return fail("binary_unavailable", "cannot checksum the supplied regular executable")
	}
	d.Timings["preparation"] = time.Since(started).Milliseconds()
	versionStarted := time.Now()
	d.Version, err = collectorVersion(runCtx, binary, filepath.Dir(configPath))
	d.Timings["version"] = time.Since(versionStarted).Milliseconds()
	if runCtx.Err() != nil {
		return fail(contextCategory(runCtx), "overall deadline or cancellation interrupted the version query")
	}
	if err != nil {
		result.RunnerWarnings = append(result.RunnerWarnings, "Collector version information could not be determined")
	}
	if len(topology.pipelines) == 0 {
		result.InconclusiveReasons = append(result.InconclusiveReasons, "fixture contains no telemetry to execute")
		return result, nil
	}
	tempDir, err := os.MkdirTemp("", "otel-policy-lab-")
	if err != nil {
		return fail("harness_io", "cannot create the private harness directory")
	}
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil && runErr == nil {
			_, runErr = fail("cleanup", "cannot remove the private harness directory")
		}
	}()
	if err := os.Chmod(tempDir, 0o700); err != nil {
		return fail("harness_io", "cannot protect the private harness directory")
	}
	sink, err := startCapture()
	if err != nil {
		return fail("capture_start", err.Error())
	}
	var process *childProcess
	var conn *grpc.ClientConn
	defer func() {
		if conn != nil {
			_ = conn.Close()
		}
		if process != nil {
			if !process.exited() {
				if err := process.stop(context.Background(), false); err != nil && runErr == nil {
					_, runErr = fail("cleanup", err.Error())
				}
			} else {
				_ = process.stop(context.Background(), false)
			}
			process.record(d)
		}
		sink.close()
		result.Output = sink.normalized()
		if category := sink.failureCategory(); category != "" && runErr == nil {
			_, runErr = fail(category, "local OTLP capture failed or exceeded its limits")
		}
	}()
	startupStarted := time.Now()
	startupCtx, startupCancel := context.WithTimeout(runCtx, r.options.StartTimeout)
	defer startupCancel()
	for attempt := 0; attempt < 3; attempt++ {
		if startupCtx.Err() != nil {
			return fail("startup_timeout", "Collector did not become ready before the startup deadline")
		}
		reservation, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return fail("receiver_address", "cannot reserve a loopback receiver endpoint")
		}
		endpoint := reservation.Addr().String()
		harness, err := topology.harness(endpoint, sink.listener.Addr().String())
		if err != nil {
			_ = reservation.Close()
			return fail("harness_config", err.Error())
		}
		hash := sha256.Sum256(harness)
		d.HarnessSHA256 = hex.EncodeToString(hash[:])
		harnessPath := filepath.Join(tempDir, "collector.yaml")
		if err := os.WriteFile(harnessPath, harness, 0o600); err != nil {
			_ = reservation.Close()
			return fail("harness_io", "cannot write the private harness configuration")
		}
		_ = reservation.Close()
		process, err = startProcess(binary, filepath.Dir(configPath), "--config", harnessPath)
		if err != nil {
			return fail("process_start", err.Error())
		}
		d.ExitStatus = "running"
		readyCtx, readyCancel := monitoredContext(startupCtx, process, sink)
		conn, err = connectReceiver(readyCtx, endpoint)
		if err == nil {
			err = waitCollectorReadiness(readyCtx, process.logs)
		}
		readyCancel()
		if err == nil && !process.exited() {
			break
		}
		if conn != nil {
			_ = conn.Close()
			conn = nil
		}
		if process.exited() {
			category := processFailure(process.logs.text())
			_ = process.stop(context.Background(), false)
			if category == "bind_conflict" && attempt < 2 {
				continue
			}
			hint := ""
			if category == "missing_component" {
				hint = missingComponentHint(process.logs.text(), topology)
			}
			return fail(category, "Collector exited during startup ("+category+")"+hint+"; verify binary components and processor configuration")
		}
		if category := sink.failureCategory(); category != "" {
			return fail(category, "local capture failed during Collector startup")
		}
		if runCtx.Err() != nil {
			return fail(contextCategory(runCtx), "overall deadline or cancellation interrupted Collector startup")
		}
		return fail("startup_timeout", "Collector did not provide OTLP and standard lifecycle readiness before the startup deadline")
	}
	d.Timings["startup"] = time.Since(startupStarted).Milliseconds()
	if conn == nil {
		return fail("bind_conflict", "could not start Collector after three loopback bind attempts")
	}
	sendStarted := time.Now()
	sendCtx, sendCancel := monitoredContext(runCtx, process, sink)
	err = submitFixture(sendCtx, conn, raw)
	sendCancel()
	d.Timings["submission"] = time.Since(sendStarted).Milliseconds()
	if err != nil {
		if runCtx.Err() != nil {
			return fail(contextCategory(runCtx), "overall deadline or cancellation interrupted OTLP submission")
		}
		if category := sink.failureCategory(); category != "" {
			return fail(category, "local capture failed during OTLP submission")
		}
		return fail("submission", err.Error())
	}
	settleStarted := time.Now()
	timer := time.NewTimer(r.options.SettleTime)
	defer timer.Stop()
	select {
	case <-runCtx.Done():
		return fail(contextCategory(runCtx), "overall deadline or cancellation interrupted settling")
	case <-process.done:
		return fail("process_exit", "Collector exited before the settling interval completed")
	case <-sink.failed:
		return fail(sink.failureCategory(), "local capture failed during settling")
	case <-timer.C:
	}
	d.Timings["settling"] = time.Since(settleStarted).Milliseconds()
	if process.exited() {
		return fail("process_exit", "Collector exited before graceful shutdown")
	}
	shutdownStarted := time.Now()
	err = process.stop(runCtx, true)
	d.Timings["shutdown"] = time.Since(shutdownStarted).Milliseconds()
	process.record(d)
	if err != nil {
		if runCtx.Err() != nil {
			return fail(contextCategory(runCtx), "overall deadline or cancellation interrupted graceful shutdown")
		}
		return fail("shutdown", err.Error())
	}
	if process.err != nil {
		return fail("process_exit", "Collector exited unsuccessfully during shutdown")
	}
	if runCtx.Err() != nil {
		return fail(contextCategory(runCtx), "overall deadline or cancellation interrupted the run")
	}
	if process.logs.hasExportFailure() {
		return fail("export", "Collector reported an export or shutdown failure")
	}
	return result, nil
}

func connectReceiver(ctx context.Context, endpoint string) (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient("passthrough:///"+endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDisableRetry(),
		grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(maxMessageBytes), grpc.MaxCallRecvMsgSize(maxMessageBytes), grpc.MaxRetryRPCBufferSize(0)),
	)
	if err != nil {
		return nil, err
	}
	conn.Connect()
	for {
		state := conn.GetState()
		if state == connectivity.Ready {
			return conn, nil
		}
		if !conn.WaitForStateChange(ctx, state) {
			_ = conn.Close()
			return nil, ctx.Err()
		}
	}
}

func monitoredContext(parent context.Context, process *childProcess, sink *captureSink) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		select {
		case <-ctx.Done():
		case <-process.done:
			cancel()
		case <-sink.failed:
			cancel()
		}
	}()
	return ctx, cancel
}

func contextCategory(ctx context.Context) string {
	if ctx.Err() == context.Canceled {
		return "canceled"
	}
	return "run_timeout"
}

func readLimited(path string, limit int64) ([]byte, error) {
	f, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("input must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("input exceeds limit")
	}
	return data, nil
}

func binaryChecksum(ctx context.Context, path string) (string, error) {
	f, err := openRegular(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 512<<20 {
		return "", fmt.Errorf("binary must be a regular file no larger than 512 MiB")
	}
	hash := sha256.New()
	buf := make([]byte, 128<<10)
	var readBytes int64
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buf)
		readBytes += int64(n)
		if readBytes > 512<<20 {
			return "", fmt.Errorf("binary grew beyond the 512 MiB limit")
		}
		if n > 0 {
			_, _ = hash.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
