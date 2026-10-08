package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andyphied/otel-policy-lab/internal/report"
)

func TestRealRunnerRequiresExplicitBinary(t *testing.T) {
	root := findRepoRoot(t)
	args := []string{"otel-policy-lab", "test", "--runner", "otelcol", "--collector-config", filepath.Join(root, "examples/collector.yaml"), "--input", filepath.Join(root, "examples/fixtures/checkout.otlp.json"), "--policy", filepath.Join(root, "examples/policy-pass.yaml")}
	if code := Run(args); code != 2 {
		t.Fatalf("implicit executable allowed: %d", code)
	}
}

func TestRealRunnerWritesErrorReportWithoutFallback(t *testing.T) {
	root := findRepoRoot(t)
	path := filepath.Join(t.TempDir(), "report.json")
	code := Run([]string{"otel-policy-lab", "test", "--runner", "otelcol", "--otelcol-bin", filepath.Join(t.TempDir(), "missing"), "--collector-config", filepath.Join(root, "examples/collector.yaml"), "--input", filepath.Join(root, "examples/fixtures/checkout.otlp.json"), "--policy", filepath.Join(root, "examples/policy-pass.yaml"), "--report", path})
	if code != 3 {
		t.Fatalf("exit=%d, want runner error", code)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rep report.Report
	if err = json.Unmarshal(data, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.SchemaVersion != 2 || rep.OverallStatus != report.StatusError || rep.Runner.Name != "otelcol" || rep.Runner.Execution.ExitStatus != "not_started" || rep.PassCount != 0 {
		t.Fatalf("incorrect error report %+v", rep)
	}
}

func TestMetadataRejectsDevicesAndHonorsCancellation(t *testing.T) {
	if _, err := os.Stat("/dev/zero"); err == nil {
		start := time.Now()
		metadata := fileMetadata(context.Background(), "/dev/zero")
		if metadata.SHA256 != "" || time.Since(start) > time.Second {
			t.Fatal("nonregular metadata read")
		}
	}
	path := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(path, []byte("fixture-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if metadata := fileMetadata(ctx, path); metadata.SHA256 != "" {
		t.Fatal("metadata ignored cancellation")
	}
}

func TestRealCLIOutcomesAndReportSecrecy(t *testing.T) {
	binary := os.Getenv("OTELCOL_BIN")
	if binary == "" {
		t.Skip("set OTELCOL_BIN for real CLI integration")
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "input.json")
	pol := filepath.Join(dir, "policy.yaml")
	config := filepath.Join(dir, "collector.yaml")
	fixture := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"hello"},"attributes":[{"key":"authorization","value":{"stringValue":"super-secret-cli-fixture"}}]}]}]}]}`
	for name, data := range map[string]string{input: fixture, pol: "version: 1\nassertions:\n  logs:\n    forbidden_attributes: [authorization]\n"} {
		if err := os.WriteFile(name, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, key string
		code      int
		status    report.Status
	}{
		{"pass", "authorization", 0, report.StatusPass},
		{"fail", "other", 1, report.StatusFail},
		{"inconclusive", "authorization", 4, report.StatusInconclusive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "receivers:\n  otlp: {}\nprocessors:\n  attributes/redact:\n    actions:\n      - {key: " + tc.key + ", action: delete}\nexporters:\n  debug: {}\nservice:\n  pipelines:\n    logs:\n      receivers: [otlp]\n      processors: [attributes/redact]\n      exporters: [debug]\n"
			if err := os.WriteFile(config, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.name == "inconclusive" {
				if err := os.WriteFile(pol, []byte("version: 1\nassertions:\n  traces:\n    preserve: ['status.code == \"ERROR\"']\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(dir, tc.name+".json")
			var code int
			out := captureStdout(t, func() {
				code = Run([]string{"otel-policy-lab", "test", "--runner", "otelcol", "--otelcol-bin", binary, "--collector-config", config, "--input", input, "--policy", pol, "--collector-settle-time", "50ms", "--report", path})
			})
			if code != tc.code {
				t.Fatalf("exit=%d want %d: %s", code, tc.code, out)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data)+out, "super-secret-cli-fixture") {
				t.Fatal("fixture secret exposed")
			}
			var rep report.Report
			if err = json.Unmarshal(data, &rep); err != nil {
				t.Fatal(err)
			}
			if rep.OverallStatus != tc.status || rep.Runner.Execution.Version == "" || rep.Runner.Execution.HarnessSHA256 == "" {
				t.Fatalf("incomplete report %+v", rep)
			}
		})
	}
}
