package evaluator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andyphied/otel-policy-lab/internal/policy"
	"github.com/andyphied/otel-policy-lab/internal/report"
	"github.com/andyphied/otel-policy-lab/internal/telemetry"
)

func TestEvaluateForbiddenReportRedactsSecrets(t *testing.T) {
	p, err := policy.Parse([]byte(`
version: 1
assertions:
  logs:
    forbidden_value_patterns:
      - bearer\s+[a-z0-9]+
    forbidden_body_patterns:
      - password=
`))
	if err != nil {
		t.Fatal(err)
	}

	output := &telemetry.Set{
		Logs: []telemetry.LogRecord{
			{
				Body: "login failed password=hunter2",
				Attributes: map[string]string{
					"authorization": "Bearer secret-token",
				},
			},
		},
	}

	results := Evaluate(p, output, output, nil)
	if len(results) != 1 || results[0].Status != report.StatusFail {
		t.Fatalf("expected fail, got %+v", results)
	}

	data, err := json.Marshal(results[0].Details)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(data)
	for _, secret := range []string{"hunter2", "secret-token", "Bearer secret-token", "login failed password=hunter2"} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("report leaked secret %q: %s", secret, serialized)
		}
	}
	if !strings.Contains(serialized, `"value_length"`) {
		t.Fatalf("expected value_length metadata, got %s", serialized)
	}
}

func TestEvaluateForbiddenMatchesDeterministic(t *testing.T) {
	p, err := policy.Parse([]byte(`
version: 1
assertions:
  logs:
    forbidden_attributes:
      - z_key
      - a_key
`))
	if err != nil {
		t.Fatal(err)
	}

	output := &telemetry.Set{
		Logs: []telemetry.LogRecord{
			{Attributes: map[string]string{"z_key": "1", "a_key": "2"}},
			{Attributes: map[string]string{"z_key": "3"}},
		},
	}

	results := Evaluate(p, output, output, nil)
	if len(results) != 1 || results[0].Status != report.StatusFail {
		t.Fatalf("expected fail, got %+v", results)
	}

	data, err := json.Marshal(results[0].Details)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"matches":[{"kind":"attribute_key","key":"a_key","pattern":"a_key","index":0},{"kind":"attribute_key","key":"z_key","pattern":"z_key","index":0},{"kind":"attribute_key","key":"z_key","pattern":"z_key","index":1}]}`
	if string(data) != want {
		t.Fatalf("deterministic mismatch\n--- got ---\n%s\n--- want ---\n%s", data, want)
	}

	golden, err := os.ReadFile(filepath.Join("testdata", "forbidden-matches.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != strings.TrimSpace(string(golden)) {
		t.Fatalf("golden mismatch\n--- got ---\n%s\n--- want ---\n%s", data, string(golden))
	}
}

func TestEvaluateCaseInsensitiveValuePatterns(t *testing.T) {
	p, err := policy.Parse([]byte(`
version: 1
assertions:
  logs:
    case_insensitive: true
    forbidden_value_patterns:
      - bearer\s+[a-z0-9]+
`))
	if err != nil {
		t.Fatal(err)
	}

	output := &telemetry.Set{
		Logs: []telemetry.LogRecord{
			{Attributes: map[string]string{"authorization": "BEARER abc123"}},
		},
	}

	results := Evaluate(p, output, output, nil)
	if len(results) != 1 || results[0].Status != report.StatusFail {
		t.Fatalf("expected fail, got %+v", results)
	}
}

func TestEvaluateForbiddenMetricLabelValues(t *testing.T) {
	p, err := policy.Parse([]byte(`
version: 1
assertions:
  metrics:
    forbidden_label_value_patterns:
      - '^user-\d+$'
`))
	if err != nil {
		t.Fatal(err)
	}

	output := &telemetry.Set{
		Metrics: []telemetry.Metric{
			{
				Name: "requests.total",
				Datapoints: []telemetry.Datapoint{
					{Labels: map[string]string{"user": "user-42"}},
				},
			},
		},
	}

	results := Evaluate(p, output, output, nil)
	if len(results) != 1 || results[0].Status != report.StatusFail {
		t.Fatalf("expected fail, got %+v", results)
	}
	if !strings.Contains(results[0].Message, `value for "user" matched`) {
		t.Fatalf("unexpected message: %s", results[0].Message)
	}
}

func TestEvaluateErrorTracePreservationAnnotatesFixtureLocal(t *testing.T) {
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
		},
	}

	warnings := []string{"fixture runner does not simulate span dropping or sampling; trace preservation checks are fixture-local"}
	results := Evaluate(p, input, input, warnings)
	if len(results) < 1 || results[0].Status != report.StatusPass {
		t.Fatalf("expected pass, got %+v", results)
	}
	if !strings.Contains(results[0].Message, "fixture-local; runner does not drop spans") {
		t.Fatalf("unexpected message: %s", results[0].Message)
	}
}

func TestEvaluateForbiddenSpanNamePatterns(t *testing.T) {
	p, err := policy.Parse([]byte(`
version: 1
assertions:
  traces:
    forbidden_value_patterns:
      - /checkout/payment
`))
	if err != nil {
		t.Fatal(err)
	}

	output := &telemetry.Set{
		Spans: []telemetry.Span{
			{Name: "POST /checkout/payment"},
		},
	}

	results := Evaluate(p, output, output, nil)
	if len(results) != 1 || results[0].Status != report.StatusFail {
		t.Fatalf("expected fail, got %+v", results)
	}
	if !strings.Contains(results[0].Message, "span name matched") {
		t.Fatalf("unexpected message: %s", results[0].Message)
	}
}

func TestEvaluateRequiredResourceReportRedactsSecrets(t *testing.T) {
	p, err := policy.Parse([]byte(`
version: 1
assertions:
  logs:
    required_resource_attributes:
      - service.name
      - deployment.environment
`))
	if err != nil {
		t.Fatal(err)
	}

	output := &telemetry.Set{
		Logs: []telemetry.LogRecord{
			{
				ResourceAttributes: map[string]string{
					"service.name":           "checkout",
					"api.key":                "super-secret-api-key-12345",
					"deployment.environment": "prod",
				},
			},
			{
				ResourceAttributes: map[string]string{
					"service.name": "checkout",
					"api.key":      "another-secret-value",
				},
			},
		},
	}

	results := Evaluate(p, output, output, nil)
	if len(results) != 1 || results[0].Status != report.StatusFail {
		t.Fatalf("expected fail, got %+v", results)
	}

	data, err := json.Marshal(results[0].Details)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(data)
	for _, secret := range []string{"super-secret-api-key-12345", "another-secret-value", "api.key"} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("report leaked secret %q: %s", secret, serialized)
		}
	}
	if strings.Contains(serialized, `"resource_attributes"`) {
		t.Fatalf("report should not include resource_attributes map: %s", serialized)
	}
}

func TestEvaluateForbiddenResourceAttributes(t *testing.T) {
	p, err := policy.Parse([]byte(`
version: 1
assertions:
  logs:
    forbidden_attributes:
      - api.key
`))
	if err != nil {
		t.Fatal(err)
	}

	output := &telemetry.Set{
		Logs: []telemetry.LogRecord{
			{
				ResourceAttributes: map[string]string{"api.key": "secret"},
				Attributes:         map[string]string{"order.id": "1"},
			},
		},
	}

	results := Evaluate(p, output, output, nil)
	if len(results) != 1 || results[0].Status != report.StatusFail {
		t.Fatalf("expected fail, got %+v", results)
	}
	if !strings.Contains(results[0].Message, "resource_key") {
		t.Fatalf("unexpected message: %s", results[0].Message)
	}
}
