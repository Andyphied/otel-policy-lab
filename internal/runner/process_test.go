//go:build darwin || linux

package runner

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/andyphied/otel-policy-lab/internal/telemetry"
	"gopkg.in/yaml.v3"
)

// The test executable is also a controlled subprocess, without external tools.
func TestCollectorProcessHelper(t *testing.T) {
	args := os.Args
	index := -1
	for i, arg := range args {
		if arg == "--collector-helper" {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	mode, dir := args[index+1], args[index+2]
	ready := func() {
		if err := os.WriteFile(filepath.Join(dir, "ready"), []byte("ready"), 0600); err != nil {
			os.Exit(91)
		}
	}
	switch mode {
	case "graceful":
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM)
		ready()
		<-signals
		os.Exit(0)
	case "ignore":
		signal.Ignore(syscall.SIGTERM)
		ready()
		for {
			time.Sleep(time.Hour)
		}
	case "no_receiver", "early_exit":
		file, err := os.Stat(args[index+3])
		if err != nil {
			os.Exit(98)
		}
		parent, err := os.Stat(filepath.Dir(args[index+3]))
		if err != nil {
			os.Exit(99)
		}
		cwd, err := os.Getwd()
		if err != nil {
			os.Exit(100)
		}
		metadata := fmt.Sprintf("%o %o\n%s\n%s", file.Mode().Perm(), parent.Mode().Perm(), args[index+3], cwd)
		if os.WriteFile(filepath.Join(dir, "harness-metadata"), []byte(metadata), 0600) != nil {
			os.Exit(101)
		}
		harness, err := os.ReadFile(args[index+3])
		if err != nil || os.WriteFile(filepath.Join(dir, "harness-copy.yaml"), harness, 0600) != nil {
			os.Exit(102)
		}
		signal.Ignore(syscall.SIGTERM)
		ready()
		if mode == "early_exit" {
			os.Exit(23)
		}
		for {
			time.Sleep(time.Hour)
		}
	case "abnormal":
		os.Exit(23)
	case "orphan":
		child := exec.Command(os.Args[0], "-test.run=^TestCollectorProcessHelper$", "--", "--collector-helper", "listener", dir)
		if child.Start() != nil {
			os.Exit(92)
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(dir, "address")); err == nil {
				os.Exit(0)
			}
			time.Sleep(5 * time.Millisecond)
		}
		_ = child.Process.Kill()
		_ = child.Wait()
		os.Exit(93)
	case "listener":
		signal.Ignore(syscall.SIGTERM)
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			os.Exit(94)
		}
		if os.WriteFile(filepath.Join(dir, "address"), []byte(listener.Addr().String()), 0600) != nil {
			os.Exit(95)
		}
		for {
			conn, err := listener.Accept()
			if err != nil {
				os.Exit(96)
			}
			_ = conn.Close()
		}
	default:
		os.Exit(97)
	}
}

func startTestCollector(t *testing.T, mode string) (*childProcess, string) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	p, err := startProcess(binary, dir, "-test.run=^TestCollectorProcessHelper$", "--", "--collector-helper", mode, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.stop(context.Background(), false) })
	return p, dir
}

func awaitProcessCondition(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", description)
		case <-tick.C:
		}
	}
}

func TestCollectorProcessGracefulShutdownReaps(t *testing.T) {
	p, dir := startTestCollector(t, "graceful")
	awaitProcessCondition(t, "helper readiness", func() bool { _, err := os.Stat(filepath.Join(dir, "ready")); return err == nil })
	if err := p.stop(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	d := &Diagnostics{}
	p.record(d)
	if p.forced || !p.exited() || d.ExitCode == nil || *d.ExitCode != 0 || d.ExitStatus != "exited" {
		t.Fatalf("graceful shutdown not reaped: %+v", d)
	}
}

func TestCollectorProcessCancellationForcesAndReaps(t *testing.T) {
	p, dir := startTestCollector(t, "ignore")
	awaitProcessCondition(t, "helper readiness", func() bool { _, err := os.Stat(filepath.Join(dir, "ready")); return err == nil })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.stop(ctx, true); err == nil {
		t.Fatal("forced graceful shutdown must report an error")
	}
	d := &Diagnostics{}
	p.record(d)
	if !p.forced || !p.exited() || d.ExitStatus != "forced" || d.ExitCode == nil {
		t.Fatalf("cancelled process not reaped: %+v", d)
	}
}

func TestCollectorProcessShutdownDeadlineForcesAndReaps(t *testing.T) {
	p, dir := startTestCollector(t, "ignore")
	awaitProcessCondition(t, "helper readiness", func() bool { _, err := os.Stat(filepath.Join(dir, "ready")); return err == nil })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := p.stop(ctx, true); err == nil {
		t.Fatal("uncooperative child must report forced shutdown")
	}
	if !p.forced || !p.exited() {
		t.Fatal("shutdown deadline did not force and reap process")
	}
}

func TestCollectorProcessAbnormalExitRecorded(t *testing.T) {
	p, _ := startTestCollector(t, "abnormal")
	awaitProcessCondition(t, "abnormal exit", p.exited)
	if err := p.stop(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	d := &Diagnostics{}
	p.record(d)
	if d.ExitCode == nil || *d.ExitCode != 23 || p.err == nil {
		t.Fatalf("lost abnormal process exit: %+v", d)
	}
}

func TestCollectorProcessExitedLeaderCleansDescendant(t *testing.T) {
	p, dir := startTestCollector(t, "orphan")
	awaitProcessCondition(t, "parent exit", p.exited)
	address, err := os.ReadFile(filepath.Join(dir, "address"))
	if err != nil {
		t.Fatalf("descendant did not start: %v", err)
	}
	if err := p.stop(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	awaitProcessCondition(t, "descendant listener release", func() bool {
		listener, err := net.Listen("tcp", string(address))
		if err != nil {
			return false
		}
		_ = listener.Close()
		return true
	})
	if p.cmd.ProcessState == nil {
		t.Fatal("leader was not reaped")
	}
}

func TestCollectorProcessStartupFailureIsSafe(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "missing-sensitive-path")
	p, err := startProcess(secretPath, t.TempDir())
	if err == nil || p != nil {
		t.Fatal("missing executable must fail before process creation")
	}
	if strings.Contains(err.Error(), "sensitive-path") {
		t.Fatal("startup error disclosed executable path")
	}
}

func TestCollectorPrivateDiagnosticsBounded(t *testing.T) {
	b := &privateBuffer{}
	first := strings.Repeat("a", maxDiagnosticBytes*2)
	if n, err := b.Write([]byte(first)); err != nil || n != len(first) {
		t.Fatal("buffer must accept full writes")
	}
	_, _ = b.Write([]byte("last-output"))
	if len(b.text()) != maxDiagnosticBytes || !strings.HasSuffix(b.text(), "last-output") {
		t.Fatal("diagnostics must keep a bounded tail")
	}
}

func TestCollectorVersionKeepsOnlySafeVersion(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "collector")
	script := "#!/bin/sh\nprintf '%s\\n' 'private-environment-value otelcol-contrib version 0.120.1 private-environment-value'\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	version, err := collectorVersion(context.Background(), binary, dir)
	if err != nil {
		t.Fatal(err)
	}
	if version != "0.120.1" {
		t.Fatalf("unsafe or unavailable version %q", version)
	}
}

func TestCollectorVersionHonorsOverallDeadline(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "collector")
	// The shell and its child must both be terminated on the parent deadline.
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nsleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := collectorVersion(ctx, binary, dir)
	if err == nil {
		t.Fatal("version timeout must fail")
	}
	if time.Since(start) > cleanupAllowance+time.Second {
		t.Fatal("version lookup exceeded parent deadline and cleanup allowance")
	}
}

func TestCollectorRunDeadlinesCleanPrivateHarness(t *testing.T) {
	for _, tc := range []struct {
		name, category, mode, exitStatus string
		startup, total                   time.Duration
		cancelWhenStarted                bool
	}{
		{"startup", "startup_timeout", "no_receiver", "forced", 150 * time.Millisecond, 5 * time.Second, false},
		{"overall", "run_timeout", "no_receiver", "forced", 10 * time.Second, 3 * time.Second, false},
		{"cancellation", "canceled", "no_receiver", "forced", 10 * time.Second, 15 * time.Second, true},
		{"early_exit", "process_exit", "early_exit", "exited", 10 * time.Second, 15 * time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			// Single-quote paths for the shell; no interpolation or command substitution.
			quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
			binary := filepath.Join(dir, "collector")
			script := "#!/bin/sh\nif [ \"$1\" = --version ]; then printf '%s\\n' 'collector version 0.120.0'; exit 0; fi\nexec " + quote(executable) + " -test.run='^TestCollectorProcessHelper$' -- --collector-helper " + tc.mode + " " + quote(dir) + " \"$2\"\n"
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(dir, "collector.yaml")
			if err := os.WriteFile(config, []byte("receivers:\n  otlp: {}\nexporters:\n  debug: {}\nservice:\n  pipelines:\n    logs:\n      receivers: [otlp]\n      exporters: [debug]\n"), 0600); err != nil {
				t.Fatal(err)
			}
			raw, err := telemetry.ParseOTLP([]byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"hello"}}]}]}]}`))
			if err != nil {
				t.Fatal(err)
			}
			r, err := New("otelcol", Options{Binary: binary, StartTimeout: tc.startup, RunTimeout: tc.total})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var result *RunResult
			completed := make(chan struct{})
			go func() {
				defer close(completed)
				result, err = r.Run(ctx, RunRequest{Raw: raw, CollectorConfigPath: config})
			}()
			if tc.cancelWhenStarted {
				awaitProcessCondition(t, "runner child readiness", func() bool { _, err := os.Stat(filepath.Join(dir, "ready")); return err == nil })
				cancel()
			}
			select {
			case <-completed:
			case <-time.After(20 * time.Second):
				t.Fatal("runner failed to complete or clean up")
			}
			if err == nil || result.Diagnostics.FailureCategory != tc.category {
				t.Fatalf("expected %s, got %v %+v", tc.category, err, result.Diagnostics)
			}
			if result.Diagnostics.ExitCode == nil || result.Diagnostics.ExitStatus != tc.exitStatus {
				t.Fatalf("deadline process not terminated/reaped: %+v", result.Diagnostics)
			}
			if tc.mode == "early_exit" && *result.Diagnostics.ExitCode != 23 {
				t.Fatalf("lost abnormal exit code: %+v", result.Diagnostics)
			}
			metadata, err := os.ReadFile(filepath.Join(dir, "harness-metadata"))
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(string(metadata), "\n")
			if len(lines) != 3 || lines[0] != "600 700" {
				t.Fatalf("unsafe harness permissions or metadata: %q", metadata)
			}
			resolvedDir, err := filepath.EvalSymlinks(dir)
			if err != nil {
				t.Fatal(err)
			}
			resolvedCWD, err := filepath.EvalSymlinks(lines[2])
			if err != nil || resolvedCWD != resolvedDir {
				t.Fatalf("processor relative resources resolve from %q, want %q", lines[2], dir)
			}
			if _, err := os.Stat(filepath.Dir(lines[1])); !os.IsNotExist(err) {
				t.Fatalf("private harness directory survived failed run: %v", err)
			}
			harness, err := os.ReadFile(filepath.Join(dir, "harness-copy.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var cfg struct {
				Exporters map[string]struct{ Endpoint string }
				Receivers map[string]struct {
					Protocols map[string]struct{ Endpoint string }
				}
			}
			if err := yaml.Unmarshal(harness, &cfg); err != nil {
				t.Fatal(err)
			}
			endpoints := []string{}
			for _, exporter := range cfg.Exporters {
				endpoints = append(endpoints, exporter.Endpoint)
			}
			for _, receiver := range cfg.Receivers {
				for _, protocol := range receiver.Protocols {
					endpoints = append(endpoints, protocol.Endpoint)
				}
			}
			if len(endpoints) != 2 {
				t.Fatalf("expected capture and receiver endpoints, got %d", len(endpoints))
			}
			for _, endpoint := range endpoints {
				listener, err := net.Listen("tcp4", endpoint)
				if err != nil {
					t.Fatalf("harness listener %s survived cleanup: %v", endpoint, err)
				}
				_ = listener.Close()
			}
		})
	}
}

func TestCollectorProcessFailureCategoriesDoNotExposeLogs(t *testing.T) {
	for _, tc := range []struct{ logs, want string }{
		{"unknown type secret-config-value", "missing_component"},
		{"error decoding secret-config-value", "invalid_config"},
		{"address already in use secret-config-value", "bind_conflict"},
		{"arbitrary secret-config-value", "process_exit"},
	} {
		if got := processFailure(tc.logs); got != tc.want {
			t.Fatalf("want %s, got %s", tc.want, got)
		}
	}
}
