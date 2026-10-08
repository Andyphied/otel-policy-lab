package evaluator

import (
	"github.com/andyphied/otel-policy-lab/internal/policy"
	"github.com/andyphied/otel-policy-lab/internal/report"
	"github.com/andyphied/otel-policy-lab/internal/telemetry"
	"testing"
)

func TestEvidenceRequiredForApplicableAssertions(t *testing.T) {
	pol := &policy.Policy{Assertions: policy.Assertions{
		Logs:    &policy.LogsAssertions{ForbiddenAttributes: []string{"secret"}},
		Traces:  &policy.TracesAssertions{Preserve: []string{`status.code == "ERROR"`}},
		Metrics: &policy.MetricsAssertions{MaxSeriesPerMetric: 1},
	}}
	empty := &telemetry.Set{}
	results := EvaluateWithOptions(pol, empty, empty, nil, Options{RequireApplicableInput: true})
	if len(results) != 3 {
		t.Fatal(results)
	}
	for _, c := range results {
		if c.Status != report.StatusInconclusive {
			t.Fatalf("vacuous pass: %+v", c)
		}
	}
	// Existing callers keep the established skipped preservation semantics.
	legacy := Evaluate(pol, empty, empty, nil)
	for _, c := range legacy {
		if c.Status != report.StatusPass {
			t.Fatal(c)
		}
	}
}

func TestUnreliableCaptureDoesNotClaimPolicyFailure(t *testing.T) {
	pol := &policy.Policy{Assertions: policy.Assertions{Traces: &policy.TracesAssertions{Preserve: []string{`status.code == "ERROR"`}}}}
	input := &telemetry.Set{Spans: []telemetry.Span{{TraceID: "a", SpanID: "b", StatusCode: "ERROR"}}}
	results := EvaluateWithOptions(pol, input, &telemetry.Set{}, nil, Options{UnreliableOutputReasons: []string{"processor decision window has not elapsed"}})
	if results[0].Status != report.StatusInconclusive || results[0].Details != nil {
		t.Fatal(results)
	}
}

func TestMaxSeriesAggregatesResourcesAndRequests(t *testing.T) {
	pol := &policy.Policy{Assertions: policy.Assertions{Metrics: &policy.MetricsAssertions{MaxSeriesPerMetric: 2}}}
	output := &telemetry.Set{Metrics: []telemetry.Metric{
		{Name: "requests", ResourceAttributes: map[string]string{"service.name": "a"}, Datapoints: []telemetry.Datapoint{{Labels: map[string]string{"path": "/one"}}}},
		{Name: "requests", ResourceAttributes: map[string]string{"service.name": "b"}, Datapoints: []telemetry.Datapoint{{Labels: map[string]string{"path": "/one"}}}},
		{Name: "requests", ResourceAttributes: map[string]string{"service.name": "a"}, Datapoints: []telemetry.Datapoint{{Labels: map[string]string{"path": "/two"}}, {Labels: map[string]string{"path": "/one"}}}},
	}}
	results := Evaluate(pol, output, output, nil)
	if len(results) != 1 || results[0].Status != report.StatusFail || results[0].Details["unique_series_count"] != 3 || results[0].Details["datapoint_count"] != 4 {
		t.Fatalf("wrong aggregated result: %+v", results)
	}
}

func TestUncertaintyIsScopedAndRetainsObservedViolations(t *testing.T) {
	pol := &policy.Policy{Assertions: policy.Assertions{
		Logs:   &policy.LogsAssertions{ForbiddenAttributes: []string{"secret"}, RequiredResourceAttributes: []string{"service.name"}},
		Traces: &policy.TracesAssertions{Preserve: []string{`status.code == "ERROR"`}},
	}}
	input := &telemetry.Set{Logs: []telemetry.LogRecord{{Attributes: map[string]string{"secret": "value"}}}, Spans: []telemetry.Span{{TraceID: "a", SpanID: "b", StatusCode: "ERROR"}}}
	output := &telemetry.Set{Logs: input.Logs}
	for _, options := range []Options{
		{UnreliableSignals: map[string][]string{"traces": {"pending decision"}}},
		{UnreliableOutputReasons: []string{"incomplete capture"}},
	} {
		results := EvaluateWithOptions(pol, input, output, nil, options)
		if results[0].Status != report.StatusFail || results[1].Status != report.StatusFail || results[2].Status != report.StatusInconclusive {
			t.Fatalf("observed violations or scoped uncertainty lost: %+v", results)
		}
		rep := report.Build(results, report.RunnerMetadata{Name: "otelcol"}, report.FileMetadata{}, report.FileMetadata{}, report.FileMetadata{}, report.SummaryStatistics{})
		if rep.OverallStatus != report.StatusFail {
			t.Fatal("uncertainty hid failure")
		}
	}
	complete := EvaluateWithOptions(pol, input, input, nil, Options{UnreliableSignals: map[string][]string{"traces": {"pending decision"}}})
	if complete[2].Status != report.StatusPass {
		t.Fatal("all captured error identities provide preservation evidence")
	}
}
