package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andyphied/otel-policy-lab/internal/telemetry"
)

func TestFixtureRunnerAppliesProcessors(t *testing.T) {
	input := &telemetry.Set{
		Logs: []telemetry.LogRecord{
			{Body: "debug: cache hit", Attributes: map[string]string{"password": "secret"}},
			{Body: "checkout complete", Attributes: map[string]string{"order.id": "1"}},
		},
		Spans: []telemetry.Span{
			{TraceID: "t1", SpanID: "s1", StatusCode: "ERROR", Attributes: map[string]string{"password": "secret"}},
		},
		Metrics: []telemetry.Metric{
			{Name: "http.server.duration", Datapoints: []telemetry.Datapoint{{Labels: map[string]string{"user.id": "u1"}}}},
		},
	}

	config := []byte(`
processors:
  attributes/redact_secrets:
    actions:
      - action: delete
        key: password
  filter/drop_debug_logs:
    logs:
      include:
        match_type: regexp
        expressions:
          - body matches ".*debug.*"
  attributes/drop_high_cardinality:
    actions:
      - action: delete
        key: user.id
service:
  pipelines:
    logs:
      processors: [attributes/redact_secrets, filter/drop_debug_logs]
    traces:
      processors: [attributes/redact_secrets]
    metrics:
      processors: [attributes/drop_high_cardinality]
`)
	configPath := filepath.Join(t.TempDir(), "collector.yaml")
	if err := os.WriteFile(configPath, config, 0o644); err != nil {
		t.Fatal(err)
	}

	r := &FixtureRunner{}
	result, err := r.Run(context.Background(), RunRequest{Fixture: input, CollectorConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Output.Logs) != 1 {
		t.Fatalf("expected 1 log after debug filter, got %d", len(result.Output.Logs))
	}
	if _, ok := result.Output.Logs[0].Attributes["password"]; ok {
		t.Fatal("password should be removed from logs")
	}
	if _, ok := result.Output.Spans[0].Attributes["password"]; ok {
		t.Fatal("password should be removed from spans")
	}
	if _, ok := result.Output.Metrics[0].Datapoints[0].Labels["user.id"]; ok {
		t.Fatal("user.id should be removed from metric labels")
	}

	var warned bool
	for _, w := range result.RunnerWarnings {
		if strings.Contains(w, "log volume reduced") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("expected log volume warning, got %v", result.RunnerWarnings)
	}
}

func TestFixtureRunnerWarnsForUnsupportedProcessor(t *testing.T) {
	config := []byte(`
processors:
  transform/redact_pii: {}
service:
  pipelines:
    traces:
      processors: [transform/redact_pii]
`)
	configPath := filepath.Join(t.TempDir(), "collector.yaml")
	if err := os.WriteFile(configPath, config, 0o644); err != nil {
		t.Fatal(err)
	}

	r := &FixtureRunner{}
	result, err := r.Run(context.Background(), RunRequest{Fixture: &telemetry.Set{}, CollectorConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.UnsupportedProcessors) != 1 || result.UnsupportedProcessors[0] != "transform/redact_pii" {
		t.Fatalf("unexpected unsupported processors: %v", result.UnsupportedProcessors)
	}
	if !strings.Contains(strings.Join(result.RunnerWarnings, "\n"), "did not simulate processor transform/redact_pii") {
		t.Fatalf("expected unsupported warning, got %v", result.RunnerWarnings)
	}
}

func TestFixtureRunnerWarnsForAttributesIncludeExclude(t *testing.T) {
	config := []byte(`
processors:
  attributes/redact_conditionally:
    include:
      match_type: strict
      services: [checkout]
    exclude:
      match_type: strict
      services: [admin]
    actions:
      - action: delete
        key: password
service:
  pipelines:
    logs:
      processors: [attributes/redact_conditionally]
`)
	configPath := filepath.Join(t.TempDir(), "collector.yaml")
	if err := os.WriteFile(configPath, config, 0o644); err != nil {
		t.Fatal(err)
	}

	r := &FixtureRunner{}
	result, err := r.Run(context.Background(), RunRequest{
		Fixture:             &telemetry.Set{Logs: []telemetry.LogRecord{{Attributes: map[string]string{"password": "secret"}}}},
		CollectorConfigPath: configPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	warnings := strings.Join(result.RunnerWarnings, "\n")
	if !strings.Contains(warnings, "include matching is not supported") || !strings.Contains(warnings, "exclude matching is not supported") {
		t.Fatalf("expected include/exclude warnings, got %v", result.RunnerWarnings)
	}
}

func TestFixtureRunnerRemovesResourceAttributes(t *testing.T) {
	input := &telemetry.Set{
		Logs: []telemetry.LogRecord{
			{
				ResourceAttributes: map[string]string{"password": "secret"},
				Attributes:         map[string]string{"order.id": "1"},
			},
		},
		Spans: []telemetry.Span{
			{
				ResourceAttributes: map[string]string{"password": "secret"},
				Attributes:         map[string]string{"order.id": "1"},
			},
		},
		Metrics: []telemetry.Metric{
			{
				Name:               "http.server.duration",
				ResourceAttributes: map[string]string{"password": "secret"},
				Datapoints:         []telemetry.Datapoint{{Labels: map[string]string{"route": "/checkout"}}},
			},
		},
	}

	config := []byte(`
processors:
  attributes/redact_secrets:
    actions:
      - action: delete
        key: password
service:
  pipelines:
    logs:
      processors: [attributes/redact_secrets]
    traces:
      processors: [attributes/redact_secrets]
    metrics:
      processors: [attributes/redact_secrets]
`)
	configPath := filepath.Join(t.TempDir(), "collector.yaml")
	if err := os.WriteFile(configPath, config, 0o644); err != nil {
		t.Fatal(err)
	}

	r := &FixtureRunner{}
	result, err := r.Run(context.Background(), RunRequest{Fixture: input, CollectorConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := result.Output.Logs[0].ResourceAttributes["password"]; ok {
		t.Fatal("password should be removed from log resource attributes")
	}
	if _, ok := result.Output.Spans[0].ResourceAttributes["password"]; ok {
		t.Fatal("password should be removed from span resource attributes")
	}
	if _, ok := result.Output.Metrics[0].ResourceAttributes["password"]; ok {
		t.Fatal("password should be removed from metric resource attributes")
	}
}

func TestFixtureRunnerWarnsTracePreservationIsFixtureLocal(t *testing.T) {
	config := []byte(`
processors:
  batch: {}
service:
  pipelines:
    traces:
      processors: [batch]
`)
	configPath := filepath.Join(t.TempDir(), "collector.yaml")
	if err := os.WriteFile(configPath, config, 0o644); err != nil {
		t.Fatal(err)
	}

	r := &FixtureRunner{}
	result, err := r.Run(context.Background(), RunRequest{Fixture: &telemetry.Set{}, CollectorConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(result.RunnerWarnings, "\n"), "trace preservation checks are fixture-local") {
		t.Fatalf("expected trace preservation confidence warning, got %v", result.RunnerWarnings)
	}
}
