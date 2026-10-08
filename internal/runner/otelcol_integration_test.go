package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andyphied/otel-policy-lab/internal/evaluator"
	"github.com/andyphied/otel-policy-lab/internal/policy"
	"github.com/andyphied/otel-policy-lab/internal/report"
	"github.com/andyphied/otel-policy-lab/internal/runner"
	"github.com/andyphied/otel-policy-lab/internal/telemetry"
)

// Standard unit tests stay offline; the integration CI job sets both variables.
func integrationBinary(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("OTELCOL_BIN")
	if binary == "" {
		if os.Getenv("OTELCOL_INTEGRATION_REQUIRED") == "1" {
			t.Fatal("OTELCOL_BIN is required for the integration suite")
		}
		t.Skip("set OTELCOL_BIN to an explicitly provisioned Collector-contrib binary")
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		t.Fatalf("resolve OTELCOL_BIN: %v", err)
	}
	return path
}

const integrationLogs = `{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}}]},"scopeLogs":[{"logRecords":[{"severityNumber":5,"body":{"stringValue":"debug: cache hit"},"attributes":[{"key":"password","value":{"stringValue":"integration-secret-do-not-report"}},{"key":"selected","value":{"boolValue":true}}]},{"severityNumber":9,"body":{"stringValue":"customer enabled debug support"},"attributes":[{"key":"password","value":{"stringValue":"integration-secret-do-not-report"}},{"key":"selected","value":{"boolValue":false}}]}]}]}]}`
const integrationTraces = `{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"00000000000000000000000000000001","spanId":"0000000000000001","name":"error-one","status":{"code":2}},{"traceId":"00000000000000000000000000000002","spanId":"0000000000000002","name":"error-two","status":{"code":2}}]}]}]}`
const integrationMetrics = `{"resourceMetrics":[{"scopeMetrics":[{"metrics":[{"name":"requests","gauge":{"dataPoints":[{"asInt":"42","attributes":[{"key":"route","value":{"stringValue":"checkout"}}]}]}}]}]}]}`

func integrationConfig(processors, signal string, order ...string) string {
	return "receivers:\n  otlp: {}\nexporters:\n  debug: {}\nprocessors:\n" + processors + "\nservice:\n  pipelines:\n    " + signal + ":\n      receivers: [otlp]\n      processors: [" + strings.Join(order, ", ") + "]\n      exporters: [debug]\n"
}

func integrationRun(t *testing.T, fixture, config string) (*runner.RunResult, error) {
	t.Helper()
	binary := integrationBinary(t)
	raw, err := telemetry.ParseOTLP([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	configPath := filepath.Join(dir, "collector.yaml")
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := runner.New("otelcol", runner.Options{Binary: binary, StartTimeout: 10 * time.Second, RunTimeout: 20 * time.Second, SettleTime: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return r.Run(context.Background(), runner.RunRequest{Raw: raw, Fixture: raw.Normalize(), CollectorConfigPath: configPath})
}

func requireIntegrationRun(t *testing.T, fixture, config string) *runner.RunResult {
	t.Helper()
	result, err := integrationRun(t, fixture, config)
	if err != nil {
		t.Fatalf("real Collector run failed: %v; diagnostics=%+v", err, result)
	}
	if result == nil || result.Output == nil {
		t.Fatal("run returned no captured output")
	}
	return result
}

func TestOTelcolIntegrationDeletionRegression(t *testing.T) {
	for _, tc := range []struct {
		name, actions string
		want          report.Status
	}{
		{"deletion", "      - key: password\n        action: delete", report.StatusPass},
		{"regression", "      - key: unrelated\n        action: delete", report.StatusFail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := requireIntegrationRun(t, integrationLogs, integrationConfig("  attributes/redact:\n    actions:\n"+tc.actions, "logs", "attributes/redact"))
			checks := evaluator.Evaluate(&policy.Policy{Assertions: policy.Assertions{Logs: &policy.LogsAssertions{ForbiddenAttributes: []string{"password"}}}}, result.Input, result.Output, nil)
			if len(checks) != 1 || checks[0].Status != tc.want {
				t.Fatalf("expected %s, got %+v", tc.want, checks)
			}
			safe, err := json.Marshal(struct {
				Checks      []report.CheckResult
				Diagnostics *runner.Diagnostics
			}{checks, result.Diagnostics})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(safe), "integration-secret-do-not-report") {
				t.Fatal("safe report data leaked a fixture secret")
			}
			if result.Diagnostics == nil || result.Diagnostics.BinarySHA256 == "" || result.Diagnostics.HarnessSHA256 == "" {
				t.Fatal("missing binary/config provenance")
			}
		})
	}
}

func TestOTelcolIntegrationActualOTTLFilter(t *testing.T) {
	for _, expression := range []string{`severity_number < SEVERITY_NUMBER_INFO`, `IsMatch(body, "^debug:")`} {
		t.Run(expression, func(t *testing.T) {
			config := integrationConfig("  filter/drop_debug:\n    error_mode: propagate\n    logs:\n      log_record:\n        - '"+expression+"'", "logs", "filter/drop_debug")
			result := requireIntegrationRun(t, integrationLogs, config)
			if len(result.Output.Logs) != 1 || result.Output.Logs[0].Body != "customer enabled debug support" {
				t.Fatalf("OTTL filter removed legitimate text or retained debug record: %+v", result.Output.Logs)
			}
		})
	}
}

func TestOTelcolIntegrationProcessorOrderAndTransform(t *testing.T) {
	processors := `  transform/mark:
    error_mode: propagate
    log_statements:
      - context: log
        statements:
          - set(attributes["marked"], true) where attributes["selected"] == true
  filter/marked:
    error_mode: propagate
    logs:
      log_record:
        - attributes["marked"] == true`
	for _, tc := range []struct {
		order []string
		want  int
	}{
		{[]string{"transform/mark", "filter/marked"}, 1},
		{[]string{"filter/marked", "transform/mark"}, 2},
	} {
		t.Run(strings.Join(tc.order, "-"), func(t *testing.T) {
			result := requireIntegrationRun(t, integrationLogs, integrationConfig(processors, "logs", tc.order...))
			if len(result.Output.Logs) != tc.want {
				t.Fatalf("processor order: want %d logs, got %d", tc.want, len(result.Output.Logs))
			}
			if tc.want == 2 {
				marked := 0
				for _, log := range result.Output.Logs {
					if log.Attributes["marked"] == "true" {
						marked++
					}
				}
				if marked != 1 {
					t.Fatalf("conditional transform marked %d records, want 1", marked)
				}
			}
		})
	}
}

func TestOTelcolIntegrationConditionalAttributes(t *testing.T) {
	processors := `  attributes/conditional:
    include:
      match_type: strict
      attributes:
        - key: selected
          value: true
    actions:
      - key: password
        action: delete`
	result := requireIntegrationRun(t, integrationLogs, integrationConfig(processors, "logs", "attributes/conditional"))
	if len(result.Output.Logs) != 2 {
		t.Fatalf("want 2 logs, got %d", len(result.Output.Logs))
	}
	for _, log := range result.Output.Logs {
		_, hasSecret := log.Attributes["password"]
		if hasSecret == (log.Attributes["selected"] == "true") {
			t.Fatal("attribute include condition was not honored")
		}
	}
}

func TestOTelcolIntegrationSamplingErrorRetention(t *testing.T) {
	for _, percent := range []int{0, 100} {
		t.Run(fmt.Sprint(percent), func(t *testing.T) {
			config := integrationConfig(fmt.Sprintf("  probabilistic_sampler:\n    hash_seed: 22\n    sampling_percentage: %d", percent), "traces", "probabilistic_sampler")
			result := requireIntegrationRun(t, integrationTraces, config)
			want := 0
			status := report.StatusFail
			if percent == 100 {
				want = 2
				status = report.StatusPass
			}
			if len(result.Input.Spans) != 2 || len(result.Output.Spans) != want {
				t.Fatalf("want retained/total %d/2, got %d/%d", want, len(result.Output.Spans), len(result.Input.Spans))
			}
			for _, span := range result.Output.Spans {
				if span.SpanID != "0000000000000001" && span.SpanID != "0000000000000002" {
					t.Fatalf("unexpected retained identity %q", span.SpanID)
				}
			}
			checks := evaluator.Evaluate(&policy.Policy{Assertions: policy.Assertions{Traces: &policy.TracesAssertions{Preserve: []string{`status.code == "ERROR"`}}}}, result.Input, result.Output, nil)
			if len(checks) != 1 || checks[0].Status != status {
				t.Fatalf("retention policy expected %s, got %+v", status, checks)
			}
		})
	}
}

func TestOTelcolIntegrationBatchShutdownFlush(t *testing.T) {
	// No size or timer trigger can flush this fixture during the 100ms settling interval.
	result := requireIntegrationRun(t, integrationLogs, integrationConfig("  batch:\n    timeout: 1h\n    send_batch_size: 10000", "logs", "batch"))
	if len(result.Output.Logs) != 2 {
		t.Fatalf("graceful shutdown lost buffered logs: got %d", len(result.Output.Logs))
	}
}

func TestOTelcolIntegrationThreeSignalsAndRepeatedOutput(t *testing.T) {
	fixture := strings.TrimSuffix(integrationLogs, "}") + "," + strings.TrimPrefix(strings.TrimSuffix(integrationTraces, "}"), "{") + "," + strings.TrimPrefix(integrationMetrics, "{")
	config := `receivers:
  otlp: {}
exporters:
  debug: {}
processors:
  batch:
    send_batch_size: 1
    send_batch_max_size: 1
service:
  pipelines:
    logs:
      receivers: [otlp]
      processors: [batch]
      exporters: [debug]
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [debug]
    metrics:
      receivers: [otlp]
      processors: [batch]
      exporters: [debug]
`
	var previous *telemetry.Set
	for i := 0; i < 3; i++ {
		result := requireIntegrationRun(t, fixture, config)
		if len(result.Output.Logs) != 2 || len(result.Output.Spans) != 2 || len(result.Output.Metrics) != 1 {
			t.Fatalf("unexpected three-signal output counts: logs=%d spans=%d metrics=%d", len(result.Output.Logs), len(result.Output.Spans), len(result.Output.Metrics))
		}
		if previous != nil && !reflect.DeepEqual(previous, result.Output) {
			t.Fatal("normalized output differs across deterministic runs")
		}
		previous = result.Output
	}
}

func TestOTelcolIntegrationProductionExporterNeverContacted(t *testing.T) {
	integrationBinary(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var contacts atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			contacts.Add(1)
			_ = conn.Close()
		}
	}()
	defer func() { _ = listener.Close(); <-done }()
	config := strings.ReplaceAll(integrationConfig("  batch: {}", "logs", "batch"), "  debug: {}", fmt.Sprintf("  otlp/trap:\n    endpoint: %s\n    tls:\n      insecure: true\n    headers:\n      authorization: source-exporter-secret", listener.Addr()))
	config = strings.ReplaceAll(config, "exporters: [debug]", "exporters: [otlp/trap]")
	result := requireIntegrationRun(t, integrationLogs, config)
	if len(result.Output.Logs) != 2 {
		t.Fatal("capture did not receive fixture")
	}
	if contacts.Load() != 0 {
		t.Fatal("source exporter endpoint was contacted")
	}
	encoded, _ := json.Marshal(result.Diagnostics)
	if strings.Contains(string(encoded), "source-exporter-secret") {
		t.Fatal("source exporter credentials leaked")
	}
}

func TestOTelcolIntegrationSafeCompatibilityErrors(t *testing.T) {
	for _, tc := range []struct{ name, processors, component, category string }{
		{"missing_component", "  nonexistent_processor: {}", "nonexistent_processor", "missing_component"},
		{"invalid_processor_config", "  attributes:\n    actions:\n      - key: password\n        action: invalid-action-secret", "attributes", "invalid_config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := integrationRun(t, integrationLogs, integrationConfig(tc.processors, "logs", tc.component))
			if err == nil {
				t.Fatal("invalid Collector configuration succeeded")
			}
			if result == nil || result.Diagnostics == nil || result.Diagnostics.FailureCategory != tc.category {
				t.Fatalf("expected compatibility category %s, got %+v (%v)", tc.category, result, err)
			}
			encoded, _ := json.Marshal(result.Diagnostics)
			if strings.Contains(err.Error()+string(encoded), "invalid-action-secret") || strings.Contains(err.Error()+string(encoded), "integration-secret-do-not-report") {
				t.Fatal("Collector error exposed secret configuration or fixture data")
			}
		})
	}
}
