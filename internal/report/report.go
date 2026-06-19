package report

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const SchemaVersion = 1

// Status represents the outcome of a check or report.
type Status string

const (
	StatusPass Status = "PASS"
	StatusFail Status = "FAIL"
	StatusWarn Status = "WARN"
)

// CheckResult is a single policy evaluation result.
type CheckResult struct {
	Status  Status         `json:"status"`
	Signal  string         `json:"signal"`
	Check   string         `json:"check"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// FileMetadata describes an input file used in a run.
type FileMetadata struct {
	Path   string `json:"path"`
	Size   int64  `json:"size_bytes"`
	SHA256 string `json:"sha256,omitempty"`
}

// SummaryStatistics captures aggregate telemetry and check stats.
type SummaryStatistics struct {
	InputLogCount      int     `json:"input_log_count"`
	OutputLogCount     int     `json:"output_log_count"`
	InputSpanCount     int     `json:"input_span_count"`
	OutputSpanCount    int     `json:"output_span_count"`
	InputMetricCount   int     `json:"input_metric_count"`
	OutputMetricCount  int     `json:"output_metric_count"`
	InputSeriesCount   int     `json:"input_unique_series_count"`
	OutputSeriesCount  int     `json:"output_unique_series_count"`
	LogVolumeChangePct float64 `json:"log_volume_change_pct,omitempty"`
}

// RunnerMetadata describes runner coverage for this report.
type RunnerMetadata struct {
	Name                  string   `json:"name"`
	SimulatedProcessors   []string `json:"simulated_processors,omitempty"`
	UnsupportedProcessors []string `json:"unsupported_processors,omitempty"`
}

// Report is the machine-readable output of a policy run.
type Report struct {
	SchemaVersion int               `json:"schema_version"`
	OverallStatus Status            `json:"overall_status"`
	PassCount     int               `json:"pass_count"`
	FailCount     int               `json:"fail_count"`
	WarnCount     int               `json:"warn_count"`
	Checks        []CheckResult     `json:"checks"`
	Input         FileMetadata      `json:"input"`
	Policy        FileMetadata      `json:"policy"`
	Collector     FileMetadata      `json:"collector_config"`
	Runner        RunnerMetadata    `json:"runner"`
	Timestamp     time.Time         `json:"timestamp"`
	Summary       SummaryStatistics `json:"summary"`
}

// Build constructs a report from check results and metadata.
func Build(checks []CheckResult, runner RunnerMetadata, input, policy, collector FileMetadata, summary SummaryStatistics) Report {
	pass, fail, warn := countStatuses(checks)
	overall := StatusPass
	if fail > 0 {
		overall = StatusFail
	} else if warn > 0 {
		overall = StatusWarn
	}
	return Report{
		SchemaVersion: SchemaVersion,
		OverallStatus: overall,
		PassCount:     pass,
		FailCount:     fail,
		WarnCount:     warn,
		Checks:        checks,
		Input:         input,
		Policy:        policy,
		Collector:     collector,
		Runner:        runner,
		Timestamp:     time.Now().UTC(),
		Summary:       summary,
	}
}

// WriteJSON writes the report to disk with pretty formatting.
func WriteJSON(path string, rep Report) error {
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// PrintTerminal renders a human-readable report to stdout.
func PrintTerminal(rep Report) {
	for _, check := range rep.Checks {
		fmt.Printf("%s %s\n", check.Status, check.Message)
	}
	fmt.Println()
	if rep.Runner.Name != "" {
		fmt.Printf("Runner: %s (simulated processors: %d, unsupported processors: %d)\n", rep.Runner.Name, len(rep.Runner.SimulatedProcessors), len(rep.Runner.UnsupportedProcessors))
	}
	fmt.Printf("Result: %s (%d pass, %d fail, %d warn)\n", rep.OverallStatus, rep.PassCount, rep.FailCount, rep.WarnCount)
}

// ExitCode returns the process exit code for a report.
func ExitCode(rep Report, failOnWarn bool) int {
	if rep.FailCount > 0 {
		return 1
	}
	if failOnWarn && rep.WarnCount > 0 {
		return 1
	}
	return 0
}

func countStatuses(checks []CheckResult) (pass, fail, warn int) {
	for _, c := range checks {
		switch c.Status {
		case StatusPass:
			pass++
		case StatusFail:
			fail++
		case StatusWarn:
			warn++
		}
	}
	return pass, fail, warn
}

// FormatSeries formats a series count with thousands separators.
func FormatSeries(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	if s != "" {
		parts = append([]string{s}, parts...)
	}
	return strings.Join(parts, ",")
}
