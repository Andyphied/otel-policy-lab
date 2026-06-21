package telemetry

import (
	"strings"
	"testing"
)

func TestParseFixture(t *testing.T) {
	data := []byte(`{
  "resourceLogs": [{
    "resource": {"attributes": [{"key": "service.name", "value": {"stringValue": "checkout"}}]},
    "scopeLogs": [{"logRecords": [{"body": {"stringValue": "hello"}, "attributes": [{"key": "order.id", "value": {"stringValue": "1"}}]}]}]
  }],
  "resourceSpans": [{
    "resource": {"attributes": [{"key": "service.name", "value": {"stringValue": "checkout"}}]},
    "scopeSpans": [{"spans": [{"traceId": "00000000000000000000000000000001", "spanId": "0000000000000001", "name": "checkout", "status": {"code": "STATUS_CODE_ERROR"}, "attributes": []}]}]
  }],
  "resourceMetrics": [{
    "resource": {"attributes": [{"key": "service.name", "value": {"stringValue": "checkout"}}]},
    "scopeMetrics": [{"metrics": [{"name": "http.server.duration", "sum": {"dataPoints": [{"attributes": [{"key": "route", "value": {"stringValue": "/a"}}]}]}}]}]
  }]
}`)

	set, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Logs) != 1 || set.Logs[0].Body != "hello" {
		t.Fatalf("logs = %+v", set.Logs)
	}
	if len(set.Spans) != 1 || set.Spans[0].StatusCode != "ERROR" {
		t.Fatalf("spans = %+v", set.Spans)
	}
	if len(set.Metrics) != 1 || len(set.Metrics[0].Datapoints) != 1 {
		t.Fatalf("metrics = %+v", set.Metrics)
	}
}

func TestCloneAndSummary(t *testing.T) {
	original := &Set{
		Logs:    []LogRecord{{Attributes: map[string]string{"a": "1"}}},
		Metrics: []Metric{{Name: "m", Datapoints: []Datapoint{{Labels: map[string]string{"x": "1"}}}}},
	}
	clone := original.Clone()
	clone.Logs[0].Attributes["a"] = "2"

	if original.Logs[0].Attributes["a"] != "1" {
		t.Fatal("clone modified original")
	}

	stats := SummaryStats(original, clone)
	if stats.InputSeriesCount != 1 || stats.OutputSeriesCount != 1 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestParseLogsOnlyFixture(t *testing.T) {
	data := []byte(`{
  "resourceLogs": [{
    "resource": {"attributes": [{"key": "service.name", "value": {"stringValue": "checkout"}}]},
    "scopeLogs": [{"logRecords": [{"body": {"stringValue": "hello"}, "attributes": []}]}]
  }]
}`)
	set, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Logs) != 1 || len(set.Spans) != 0 || len(set.Metrics) != 0 {
		t.Fatalf("set = %+v", set)
	}
}

func TestParseTracesOnlyFixture(t *testing.T) {
	data := []byte(`{
  "resourceSpans": [{
    "resource": {"attributes": [{"key": "service.name", "value": {"stringValue": "checkout"}}]},
    "scopeSpans": [{"spans": [{"traceId": "00000000000000000000000000000001", "spanId": "0000000000000001", "name": "checkout", "status": {"code": "STATUS_CODE_ERROR"}, "attributes": []}]}]
  }]
}`)
	set, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Spans) != 1 || len(set.Logs) != 0 || len(set.Metrics) != 0 {
		t.Fatalf("set = %+v", set)
	}
}

func TestParseMetricsOnlyFixture(t *testing.T) {
	data := []byte(`{
  "resourceMetrics": [{
    "resource": {"attributes": [{"key": "service.name", "value": {"stringValue": "checkout"}}]},
    "scopeMetrics": [{"metrics": [{"name": "http.server.duration", "sum": {"dataPoints": [{"attributes": [{"key": "route", "value": {"stringValue": "/a"}}]}]}}]}]
  }]
}`)
	set, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Metrics) != 1 || len(set.Logs) != 0 || len(set.Spans) != 0 {
		t.Fatalf("set = %+v", set)
	}
}

func TestParseRejectsInvalidJSON(t *testing.T) {
	_, err := Parse([]byte(`{"resourceLogs": [`))
	if err == nil || !strings.Contains(err.Error(), "parse fixture json") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestParseRejectsInvalidTraceID(t *testing.T) {
	data := []byte(`{
  "resourceSpans": [{
    "resource": {},
    "scopeSpans": [{"spans": [{"traceId": "bad", "spanId": "0000000000000001", "name": "checkout", "status": {"code": "STATUS_CODE_ERROR"}}]}]
  }]
}`)
	_, err := Parse(data)
	if err == nil || !strings.Contains(err.Error(), "parse resourceSpans") {
		t.Fatalf("expected resourceSpans parse error, got %v", err)
	}
}
