package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/andyphied/otel-policy-lab/internal/runner"
)

func TestRealOutcomesAndSchema(t *testing.T) {
	for _, tt := range []struct {
		name     string
		statuses []Status
		overall  Status
		exit     int
	}{
		{"pass", []Status{StatusPass}, StatusPass, 0},
		{"warning", []Status{StatusPass, StatusWarn}, StatusPass, 0},
		{"fail", []Status{StatusFail}, StatusFail, 1},
		{"inconclusive", []Status{StatusPass, StatusInconclusive}, StatusInconclusive, 4},
		{"failure with incomplete evidence", []Status{StatusFail, StatusInconclusive}, StatusFail, 1},
		{"error", []Status{StatusFail, StatusError}, StatusError, 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var checks []CheckResult
			for _, s := range tt.statuses {
				checks = append(checks, CheckResult{Status: s})
			}
			rep := Build(checks, RunnerMetadata{Name: "otelcol", Execution: &runner.Diagnostics{Version: "0.120.1", ExitStatus: "exited"}}, FileMetadata{}, FileMetadata{}, FileMetadata{}, SummaryStatistics{})
			if rep.SchemaVersion != 2 || rep.OverallStatus != tt.overall || ExitCode(rep, false) != tt.exit {
				t.Fatalf("unexpected report %+v", rep)
			}
			data, err := json.Marshal(rep)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), `"version":"0.120.1"`) {
				t.Fatal(string(data))
			}
		})
	}
}
