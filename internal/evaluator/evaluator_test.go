package evaluator

import (
	"strings"
	"testing"

	"github.com/andyphied/otel-policy-lab/internal/policy"
	"github.com/andyphied/otel-policy-lab/internal/report"
	"github.com/andyphied/otel-policy-lab/internal/telemetry"
)

func TestEvaluateForbiddenLogAttributes(t *testing.T) {
	p, err := policy.Parse([]byte(`
version: 1
assertions:
  logs:
    forbidden_attributes:
      - password
`))
	if err != nil {
		t.Fatal(err)
	}

	output := &telemetry.Set{
		Logs: []telemetry.LogRecord{
			{Attributes: map[string]string{"password": "secret"}},
		},
	}

	results := Evaluate(p, output, output, nil)
	if len(results) != 1 || results[0].Status != report.StatusFail {
		t.Fatalf("expected fail, got %+v", results)
	}
}

func TestEvaluateRequiredLogResourceAttributes(t *testing.T) {
	p, err := policy.Parse([]byte(`
version: 1
assertions:
  logs:
    required_resource_attributes:
      - service.name
`))
	if err != nil {
		t.Fatal(err)
	}

	output := &telemetry.Set{
		Logs: []telemetry.LogRecord{
			{ResourceAttributes: map[string]string{"service.name": "checkout"}},
		},
	}

	results := Evaluate(p, output, output, nil)
	if len(results) != 1 || results[0].Status != report.StatusPass {
		t.Fatalf("expected pass, got %+v", results)
	}
	if !strings.Contains(results[0].Message, "service.name") {
		t.Fatalf("unexpected message: %s", results[0].Message)
	}
}

func TestEvaluateRequiredLogResourceAttributesFailsWhenAnyRecordMissing(t *testing.T) {
	p, err := policy.Parse([]byte(`
version: 1
assertions:
  logs:
    required_resource_attributes:
      - service.name
`))
	if err != nil {
		t.Fatal(err)
	}

	output := &telemetry.Set{
		Logs: []telemetry.LogRecord{
			{ResourceAttributes: map[string]string{"service.name": "checkout"}},
			{ResourceAttributes: map[string]string{"deployment.environment": "prod"}},
		},
	}

	results := Evaluate(p, output, output, nil)
	if len(results) != 1 || results[0].Status != report.StatusFail {
		t.Fatalf("expected fail, got %+v", results)
	}
	if !strings.Contains(results[0].Message, "service.name (1/2 records)") {
		t.Fatalf("unexpected message: %s", results[0].Message)
	}
}

func TestEvaluateForbiddenLogDataMatchesKeysValuesAndBodies(t *testing.T) {
	p, err := policy.Parse([]byte(`
version: 1
assertions:
  logs:
    case_insensitive: true
    forbidden_attributes:
      - authorization
      - http.request.header.*
    forbidden_value_patterns:
      - (?i)bearer\s+[a-z0-9]+
    forbidden_body_patterns:
      - (?i)password=
`))
	if err != nil {
		t.Fatal(err)
	}

	output := &telemetry.Set{
		Logs: []telemetry.LogRecord{
			{
				Body: "login failed password=hunter2",
				Attributes: map[string]string{
					"Authorization":              "Bearer abc123",
					"http.request.header.cookie": "session=abc",
				},
			},
		},
	}

	results := Evaluate(p, output, output, nil)
	if len(results) != 1 || results[0].Status != report.StatusFail {
		t.Fatalf("expected fail, got %+v", results)
	}
	if !strings.Contains(results[0].Message, "forbidden log data exported") {
		t.Fatalf("unexpected message: %s", results[0].Message)
	}
}

func TestEvaluateErrorTracePreservation(t *testing.T) {
	p, err := policy.Parse([]byte(`
version: 1
assertions:
  traces:
    preserve:
      - status.code == "ERROR"
`))
	if err != nil {
		t.Fatal(err)
	}

	input := &telemetry.Set{
		Spans: []telemetry.Span{
			{TraceID: "t1", SpanID: "s1", StatusCode: "ERROR"},
			{TraceID: "t2", SpanID: "s2", StatusCode: "ERROR"},
		},
	}
	output := &telemetry.Set{
		Spans: []telemetry.Span{
			{TraceID: "t1", SpanID: "s1", StatusCode: "ERROR"},
		},
	}

	results := Evaluate(p, input, output, nil)
	if len(results) != 1 || results[0].Status != report.StatusFail {
		t.Fatalf("expected fail, got %+v", results)
	}
	if !strings.Contains(results[0].Message, "50%") {
		t.Fatalf("unexpected message: %s", results[0].Message)
	}
}

func TestEvaluateMaxSeriesPerMetric(t *testing.T) {
	p, err := policy.Parse([]byte(`
version: 1
assertions:
  metrics:
    max_series_per_metric: 10
`))
	if err != nil {
		t.Fatal(err)
	}

	dps := make([]telemetry.Datapoint, 15)
	for i := range dps {
		dps[i] = telemetry.Datapoint{Labels: map[string]string{"route": string(rune('a' + i))}}
	}
	output := &telemetry.Set{
		Metrics: []telemetry.Metric{
			{Name: "http.server.duration", Datapoints: dps},
		},
	}

	results := Evaluate(p, output, output, nil)
	if len(results) != 1 || results[0].Status != report.StatusFail {
		t.Fatalf("expected fail, got %+v", results)
	}
	if !strings.Contains(results[0].Message, "http.server.duration") {
		t.Fatalf("unexpected message: %s", results[0].Message)
	}
}

func TestEvaluateMaxSeriesPerMetricCountsUniqueSeries(t *testing.T) {
	p, err := policy.Parse([]byte(`
version: 1
assertions:
  metrics:
    max_series_per_metric: 1
`))
	if err != nil {
		t.Fatal(err)
	}

	output := &telemetry.Set{
		Metrics: []telemetry.Metric{
			{
				Name:               "http.server.duration",
				ResourceAttributes: map[string]string{"service.name": "checkout"},
				Datapoints: []telemetry.Datapoint{
					{Labels: map[string]string{"route": "/checkout"}},
					{Labels: map[string]string{"route": "/checkout"}},
				},
			},
		},
	}

	results := Evaluate(p, output, output, nil)
	if len(results) != 1 || results[0].Status != report.StatusPass {
		t.Fatalf("expected pass for duplicate datapoints in one series, got %+v", results)
	}
}

func TestEvaluateRunnerWarnings(t *testing.T) {
	p, err := policy.Parse([]byte(`
version: 1
assertions:
  logs:
    forbidden_attributes:
      - password
`))
	if err != nil {
		t.Fatal(err)
	}

	results := Evaluate(p, &telemetry.Set{}, &telemetry.Set{}, []string{"log volume reduced by 71%; verify debug logs are not over-filtered"})
	var warn bool
	for _, r := range results {
		if r.Status == report.StatusWarn {
			warn = true
		}
	}
	if !warn {
		t.Fatalf("expected warning result, got %+v", results)
	}
}
