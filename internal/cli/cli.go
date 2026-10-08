package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/andyphied/otel-policy-lab/internal/collector"
	"github.com/andyphied/otel-policy-lab/internal/evaluator"
	"github.com/andyphied/otel-policy-lab/internal/policy"
	"github.com/andyphied/otel-policy-lab/internal/report"
	"github.com/andyphied/otel-policy-lab/internal/runner"
	"github.com/andyphied/otel-policy-lab/internal/telemetry"
)

// Version is set by the main package or linker flags.
var Version = "dev"

const usage = `otel-policy-lab - test OpenTelemetry Collector configs against policy assertions

Usage:
  otel-policy-lab test --collector-config <path> --input <path> --policy <path> [--report <path>] [--runner fixture|otelcol] [--otelcol-bin <path>] [--fail-on-warn] [--validate-collector]
  otel-policy-lab validate --collector-config <path> [--otelcol-bin otelcol]
  otel-policy-lab --version

Exit codes:
  0  all checks passed
  1  policy failure (or warning with --fail-on-warn)
  2  usage or input parsing error
  3  pipeline runner error
  4  inconclusive policy result
`

// Run executes the CLI with the provided arguments.
func Run(args []string) int {
	if len(args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}

	switch args[1] {
	case "test":
		return runTest(args[2:])
	case "validate":
		return runValidate(args[2:])
	case "-v", "--version", "version":
		fmt.Println(Version)
		return 0
	case "-h", "--help", "help":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", args[1], usage)
		return 2
	}
}

func runTest(args []string) int {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	collectorConfig := fs.String("collector-config", "", "path to OpenTelemetry Collector config YAML")
	input := fs.String("input", "", "path to OTLP JSON telemetry fixture")
	policyPath := fs.String("policy", "", "path to policy YAML")
	reportPath := fs.String("report", "", "optional path to write JSON report")
	runnerName := fs.String("runner", "fixture", "pipeline runner to use (fixture or otelcol)")
	failOnWarn := fs.Bool("fail-on-warn", false, "exit non-zero on warnings")
	validateCollector := fs.Bool("validate-collector", false, "run otelcol validate before policy evaluation")
	otelcolBin := fs.String("otelcol-bin", "otelcol", "Collector binary (must be explicitly supplied for runner otelcol)")
	startTimeout := fs.Duration("collector-start-timeout", 10*time.Second, "real Collector startup timeout")
	runTimeout := fs.Duration("collector-run-timeout", 30*time.Second, "real Collector total run timeout")
	settleTime := fs.Duration("collector-settle-time", 500*time.Millisecond, "settling time before graceful Collector shutdown")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *collectorConfig == "" || *input == "" || *policyPath == "" {
		fmt.Fprintln(os.Stderr, "error: --collector-config, --input, and --policy are required")
		fs.Usage()
		return 2
	}

	real := *runnerName == "otelcol"
	explicitBinary := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "otelcol-bin" {
			explicitBinary = true
		}
	})
	if real && (!explicitBinary || strings.TrimSpace(*otelcolBin) == "") {
		fmt.Fprintln(os.Stderr, "error: --runner otelcol requires an explicit --otelcol-bin path; no binary is downloaded or selected automatically")
		return 2
	}
	if real && (*startTimeout <= 0 || *runTimeout <= 0 || *settleTime < 0 || *settleTime >= *runTimeout) {
		fmt.Fprintln(os.Stderr, "error: Collector timeouts must be positive; settle time must be non-negative and shorter than run timeout")
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	// The real runner starts only its isolated configuration. Validating the
	// source here would load production components/providers outside the harness.
	if *validateCollector && !real {
		if _, err := collector.ValidateConfig(ctx, *otelcolBin, *collectorConfig); err != nil {
			fmt.Fprintf(os.Stderr, "error: collector validation failed: %v\n", err)
			return 2
		}
	}

	pol, err := policy.Load(*policyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: load policy: %v\n", err)
		return 2
	}

	var fixture *telemetry.Set
	var raw *telemetry.OTLP
	if real {
		raw, err = telemetry.LoadOTLP(*input)
		if err == nil {
			fixture = raw.Normalize()
		}
	} else {
		fixture, err = telemetry.Load(*input)
	}
	if err != nil {
		if real {
			fmt.Fprintln(os.Stderr, "error: could not read or parse OTLP fixture")
		} else {
			fmt.Fprintf(os.Stderr, "error: load fixture: %v\n", err)
		}
		return 2
	}

	var options []runner.Options
	if real {
		options = append(options, runner.Options{Binary: *otelcolBin, StartTimeout: *startTimeout, RunTimeout: *runTimeout, SettleTime: *settleTime})
	}
	r, err := runner.New(*runnerName, options...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: create runner: %v\n", err)
		return 2
	}

	result, runErr := r.Run(ctx, runner.RunRequest{
		FixturePath:         *input,
		CollectorConfigPath: *collectorConfig,
		Fixture:             fixture,
		Raw:                 raw,
	})
	if runErr != nil && !real {
		fmt.Fprintf(os.Stderr, "error: run pipeline: %v\n", runErr)
		return 3
	}
	if result == nil {
		result = &runner.RunResult{RunnerName: *runnerName, Input: fixture, Output: &telemetry.Set{}}
	}
	if result.Input == nil {
		result.Input = fixture
	}
	if real && runErr != nil {
		// Input was already parsed by the CLI even if binary/config preflight
		// failed before the runner could attach its normalized input.
		result.Input = fixture
	}
	if result.Output == nil {
		result.Output = &telemetry.Set{}
	}

	var checks []report.CheckResult
	if runErr != nil {
		checks = []report.CheckResult{{Status: report.StatusError, Signal: "pipeline", Check: "runner_execution", Message: runErr.Error()}}
	} else {
		uncertain := result.InconclusiveReasons
		if len(result.InconclusiveSignals) > 0 {
			uncertain = nil
		}
		checks = evaluator.EvaluateWithOptions(pol, result.Input, result.Output, result.RunnerWarnings, evaluator.Options{
			RequireApplicableInput: real, UnreliableOutputReasons: uncertain, UnreliableSignals: result.InconclusiveSignals,
		})
	}
	summary := buildSummary(result.Input, result.Output)
	metadata := report.RunnerMetadata{
		Name: result.RunnerName, SimulatedProcessors: result.SimulatedProcessors,
		UnsupportedProcessors: result.UnsupportedProcessors, Execution: result.Diagnostics,
	}
	if real {
		metadata.Scope = "fixture-local processor chains; production receivers, exporters and backend delivery excluded"
	}

	rep := report.Build(
		checks,
		metadata,
		fileMetadata(ctx, *input),
		fileMetadata(ctx, *policyPath),
		fileMetadata(ctx, *collectorConfig),
		summary,
	)

	report.PrintTerminal(rep)

	if *reportPath != "" {
		if err := report.WriteJSON(*reportPath, rep); err != nil {
			fmt.Fprintf(os.Stderr, "error: write report: %v\n", err)
			return 2
		}
	}

	return report.ExitCode(rep, *failOnWarn)
}

func runValidate(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	collectorConfig := fs.String("collector-config", "", "path to OpenTelemetry Collector config YAML")
	otelcolBin := fs.String("otelcol-bin", "otelcol", "otelcol binary")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *collectorConfig == "" {
		fmt.Fprintln(os.Stderr, "error: --collector-config is required")
		fs.Usage()
		return 2
	}

	result, err := collector.ValidateConfig(context.Background(), *otelcolBin, *collectorConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL collector config validation failed: %v\n", err)
		return 2
	}

	fmt.Printf("PASS collector config validated with %s\n", result.Command)
	if strings.TrimSpace(result.Output) != "" {
		fmt.Print(result.Output)
	}
	return 0
}

func buildSummary(input, output *telemetry.Set) report.SummaryStatistics {
	stats := telemetry.SummaryStats(input, output)
	summary := report.SummaryStatistics{
		InputLogCount:     stats.InputLogCount,
		OutputLogCount:    stats.OutputLogCount,
		InputSpanCount:    stats.InputSpanCount,
		OutputSpanCount:   stats.OutputSpanCount,
		InputMetricCount:  stats.InputMetricCount,
		OutputMetricCount: stats.OutputMetricCount,
		InputSeriesCount:  stats.InputSeriesCount,
		OutputSeriesCount: stats.OutputSeriesCount,
	}
	if stats.InputLogCount > 0 {
		change := float64(stats.OutputLogCount-stats.InputLogCount) / float64(stats.InputLogCount) * 100
		summary.LogVolumeChangePct = change
	}
	return summary
}

func fileMetadata(ctx context.Context, path string) report.FileMetadata {
	info, err := os.Stat(path)
	if err != nil {
		return report.FileMetadata{Path: path}
	}
	// Failed runner inputs may be devices, FIFOs or oversized files. Metadata
	// collection must not retry an unbounded read after the runner rejected them.
	const maxMetadataBytes = 64 << 20
	if !info.Mode().IsRegular() || info.Size() > maxMetadataBytes || ctx.Err() != nil {
		return report.FileMetadata{Path: path, Size: info.Size()}
	}
	f, err := os.Open(path)
	if err != nil {
		return report.FileMetadata{Path: path, Size: info.Size()}
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() {
		return report.FileMetadata{Path: path, Size: info.Size()}
	}
	hash := sha256.New()
	buf := make([]byte, 32<<10)
	var total int64
	for {
		if ctx.Err() != nil {
			return report.FileMetadata{Path: path, Size: info.Size()}
		}
		n, readErr := f.Read(buf)
		total += int64(n)
		if total > maxMetadataBytes {
			return report.FileMetadata{Path: path, Size: info.Size()}
		}
		_, _ = hash.Write(buf[:n])
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return report.FileMetadata{Path: path, Size: info.Size()}
		}
	}
	return report.FileMetadata{
		Path:   path,
		Size:   info.Size(),
		SHA256: hex.EncodeToString(hash.Sum(nil)),
	}
}
