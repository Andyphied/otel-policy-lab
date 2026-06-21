package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunExampleIntegration(t *testing.T) {
	root := findRepoRoot(t)
	args := []string{
		"otel-policy-lab", "test",
		"--collector-config", filepath.Join(root, "examples/collector.yaml"),
		"--input", filepath.Join(root, "examples/fixtures/checkout.otlp.json"),
		"--policy", filepath.Join(root, "examples/policy.yaml"),
		"--report", filepath.Join(t.TempDir(), "report.json"),
	}

	code := Run(args)
	if code != 1 {
		t.Fatalf("Run() = %d, want 1 (policy failure on metric series)", code)
	}
}

func TestRunExampleFailingOutputGolden(t *testing.T) {
	root := findRepoRoot(t)
	args := []string{
		"otel-policy-lab", "test",
		"--collector-config", filepath.Join(root, "examples/collector.yaml"),
		"--input", filepath.Join(root, "examples/fixtures/checkout.otlp.json"),
		"--policy", filepath.Join(root, "examples/policy.yaml"),
		"--report", filepath.Join(t.TempDir(), "report.json"),
	}

	var code int
	out := captureStdout(t, func() {
		code = Run(args)
	})
	if code != 1 {
		t.Fatalf("Run() = %d, want 1", code)
	}
	assertGolden(t, "failing-output.golden", out)
}

func TestRunExamplePassingOutputGolden(t *testing.T) {
	root := findRepoRoot(t)
	args := []string{
		"otel-policy-lab", "test",
		"--collector-config", filepath.Join(root, "examples/collector.yaml"),
		"--input", filepath.Join(root, "examples/fixtures/checkout.otlp.json"),
		"--policy", filepath.Join(root, "examples/policy-pass.yaml"),
		"--report", filepath.Join(t.TempDir(), "report.json"),
	}

	var code int
	out := captureStdout(t, func() {
		code = Run(args)
	})
	if code != 0 {
		t.Fatalf("Run() = %d, want 0", code)
	}
	assertGolden(t, "passing-output.golden", out)
}

func TestRunMissingRequiredFlags(t *testing.T) {
	if code := Run([]string{"otel-policy-lab", "test"}); code != 2 {
		t.Fatalf("Run() = %d, want 2", code)
	}
}

func TestRunMissingPolicyFile(t *testing.T) {
	root := findRepoRoot(t)
	code := Run([]string{
		"otel-policy-lab", "test",
		"--collector-config", filepath.Join(root, "examples/collector.yaml"),
		"--input", filepath.Join(root, "examples/fixtures/checkout.otlp.json"),
		"--policy", filepath.Join(root, "examples/missing-policy.yaml"),
	})
	if code != 2 {
		t.Fatalf("Run() = %d, want 2", code)
	}
}

func TestRunMissingFixtureFile(t *testing.T) {
	root := findRepoRoot(t)
	code := Run([]string{
		"otel-policy-lab", "test",
		"--collector-config", filepath.Join(root, "examples/collector.yaml"),
		"--input", filepath.Join(root, "examples/fixtures/missing.otlp.json"),
		"--policy", filepath.Join(root, "examples/policy.yaml"),
	})
	if code != 2 {
		t.Fatalf("Run() = %d, want 2", code)
	}
}

func TestRunVersion(t *testing.T) {
	if code := Run([]string{"otel-policy-lab", "--version"}); code != 0 {
		t.Fatalf("Run() = %d, want 0", code)
	}
}

func TestValidateMissingRequiredFlags(t *testing.T) {
	if code := Run([]string{"otel-policy-lab", "validate"}); code != 2 {
		t.Fatalf("Run() = %d, want 2", code)
	}
}

func TestValidateMissingOtelcolBinary(t *testing.T) {
	root := findRepoRoot(t)
	code := Run([]string{
		"otel-policy-lab", "validate",
		"--collector-config", filepath.Join(root, "examples/collector.yaml"),
		"--otelcol-bin", "otelcol-definitely-not-installed-for-test",
	})
	if code != 2 {
		t.Fatalf("Run() = %d, want 2", code)
	}
}

func TestValidateSuccessWhenOtelcolPresent(t *testing.T) {
	if _, err := exec.LookPath("otelcol"); err != nil {
		t.Skip("otelcol not installed")
	}

	root := findRepoRoot(t)
	code := Run([]string{
		"otel-policy-lab", "validate",
		"--collector-config", filepath.Join(root, "examples/collector.yaml"),
	})
	if code != 0 {
		t.Fatalf("Run() = %d, want 0", code)
	}
}

func TestRunJSONReportRedactsRequiredResourceSecrets(t *testing.T) {
	root := findRepoRoot(t)
	testdata := filepath.Join(root, "internal/cli/testdata")
	reportPath := filepath.Join(t.TempDir(), "report.json")

	code := Run([]string{
		"otel-policy-lab", "test",
		"--collector-config", filepath.Join(testdata, "report-secret-collector.yaml"),
		"--input", filepath.Join(testdata, "secret-redaction-fixture.otlp.json"),
		"--policy", filepath.Join(testdata, "report-secret-policy.yaml"),
		"--report", reportPath,
	})
	if code != 1 {
		t.Fatalf("Run() = %d, want 1", code)
	}

	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(data)
	for _, secret := range []string{"super-secret-api-key-12345", "api.key"} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("report leaked secret %q: %s", secret, serialized)
		}
	}
	if strings.Contains(serialized, `"resource_attributes"`) {
		t.Fatalf("report should not include resource_attributes map: %s", serialized)
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root")
		}
		dir = parent
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, string(want))
	}
}
