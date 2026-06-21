package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildAndExitCode(t *testing.T) {
	checks := []CheckResult{
		{Status: StatusPass, Message: "ok"},
		{Status: StatusFail, Message: "bad"},
		{Status: StatusWarn, Message: "careful"},
	}
	rep := Build(checks, RunnerMetadata{Name: "fixture"}, FileMetadata{Path: "in.json"}, FileMetadata{Path: "policy.yaml"}, FileMetadata{Path: "collector.yaml"}, SummaryStatistics{})

	if rep.OverallStatus != StatusFail {
		t.Fatalf("OverallStatus = %s, want FAIL", rep.OverallStatus)
	}
	if rep.PassCount != 1 || rep.FailCount != 1 || rep.WarnCount != 1 {
		t.Fatalf("unexpected counts: %+v", rep)
	}
	if ExitCode(rep, false) != 1 {
		t.Fatalf("expected exit code 1")
	}
	if ExitCode(rep, true) != 1 {
		t.Fatalf("expected exit code 1 with fail-on-warn")
	}

	passWarn := Build([]CheckResult{{Status: StatusPass, Message: "ok"}, {Status: StatusWarn, Message: "careful"}}, RunnerMetadata{Name: "fixture"}, FileMetadata{}, FileMetadata{}, FileMetadata{}, SummaryStatistics{})
	if ExitCode(passWarn, false) != 0 {
		t.Fatalf("expected exit code 0 without fail-on-warn")
	}
	if ExitCode(passWarn, true) != 1 {
		t.Fatalf("expected exit code 1 with fail-on-warn only")
	}
}

func TestWriteJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	rep := Build([]CheckResult{{Status: StatusPass, Signal: "logs", Check: "forbidden_attributes", Message: "ok"}}, RunnerMetadata{Name: "fixture", SimulatedProcessors: []string{"batch"}},
		FileMetadata{Path: "in.json", Size: 10},
		FileMetadata{Path: "policy.yaml", Size: 20},
		FileMetadata{Path: "collector.yaml", Size: 30},
		SummaryStatistics{InputLogCount: 7, OutputLogCount: 2},
	)
	rep.Timestamp = time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)

	if err := WriteJSON(path, rep); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "report.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(want) {
		t.Fatalf("golden mismatch\n--- got ---\n%s\n--- want ---\n%s", data, want)
	}
	if decoded.OverallStatus != StatusPass {
		t.Fatalf("decoded status = %s", decoded.OverallStatus)
	}
	if decoded.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", decoded.SchemaVersion, SchemaVersion)
	}
	if decoded.Summary.InputLogCount != 7 {
		t.Fatalf("summary = %+v", decoded.Summary)
	}
}

func TestFormatSeries(t *testing.T) {
	if FormatSeries(28441) != "28,441" {
		t.Fatalf("FormatSeries(28441) = %s", FormatSeries(28441))
	}
}

func TestPrintTerminal(t *testing.T) {
	rep := Build([]CheckResult{
		{Status: StatusPass, Message: "PASS line"},
		{Status: StatusFail, Message: "FAIL line"},
	}, RunnerMetadata{Name: "fixture", SimulatedProcessors: []string{"batch"}, UnsupportedProcessors: []string{"transform/redact"}}, FileMetadata{}, FileMetadata{}, FileMetadata{}, SummaryStatistics{})

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	PrintTerminal(rep)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old

	buf := make([]byte, 4096)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	out := string(buf[:n])
	if !strings.Contains(out, "PASS line") || !strings.Contains(out, "FAIL line") {
		t.Fatalf("unexpected output: %s", out)
	}
	if !strings.Contains(out, "Runner: fixture (simulated processors: 1, unsupported processors: 1)") {
		t.Fatalf("missing runner coverage line: %s", out)
	}
}
