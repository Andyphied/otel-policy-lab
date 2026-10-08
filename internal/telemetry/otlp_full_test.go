package telemetry

import (
	"reflect"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func TestOTLPRetainsProcessorInputs(t *testing.T) {
	raw, err := ParseOTLP([]byte(`{
 "resourceLogs":[{"scopeLogs":[{"scope":{"name":"instrumentation","version":"1.2"},"logRecords":[{"timeUnixNano":"123456789","severityNumber":17,"severityText":"ERROR","body":{"intValue":"42"},"attributes":[{"key":"enabled","value":{"boolValue":true}}]}]}]}],
 "resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"00000000000000000000000000000001","spanId":"0000000000000001","parentSpanId":"0000000000000002","startTimeUnixNano":"123","endTimeUnixNano":"456","events":[{"name":"event","timeUnixNano":"321"}]}]}]}],
 "resourceMetrics":[{"scopeMetrics":[{"metrics":[{"name":"total","unit":"ms","sum":{"aggregationTemporality":2,"isMonotonic":true,"dataPoints":[{"asInt":"123","timeUnixNano":"456"}]}}]}]}]
 }`))
	if err != nil {
		t.Fatal(err)
	}
	before, err := new(plog.JSONMarshaler).MarshalLogs(raw.Logs)
	if err != nil {
		t.Fatal(err)
	}
	view := raw.Normalize()
	if view.Logs[0].Body != "42" {
		t.Fatal(view)
	}
	log := raw.Logs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	if log.SeverityNumber() != plog.SeverityNumberError || uint64(log.Timestamp()) != 123456789 {
		t.Fatal("lost log fields")
	}
	v, _ := log.Attributes().Get("enabled")
	if !v.Bool() {
		t.Fatal("lost attribute type")
	}
	span := raw.Traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	if span.Events().Len() != 1 || uint64(span.StartTimestamp()) != 123 || span.ParentSpanID().String() != "0000000000000002" {
		t.Fatal("lost span fields")
	}
	metric := raw.Metrics.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
	if metric.Type() != pmetric.MetricTypeSum || metric.Sum().DataPoints().At(0).IntValue() != 123 || !metric.Sum().IsMonotonic() {
		t.Fatal("lost metric fields")
	}
	after, err := new(plog.JSONMarshaler).MarshalLogs(raw.Logs)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("normalization changed transport data")
	}
}

func TestCanonicalizeCaptureFragments(t *testing.T) {
	resource := map[string]string{"service.name": "example"}
	a := Datapoint{Labels: map[string]string{"route": "a"}}
	b := Datapoint{Labels: map[string]string{"route": "b"}}
	combined := &Set{Logs: []LogRecord{{Body: "a"}, {Body: "b"}}, Metrics: []Metric{{Name: "m", ResourceAttributes: resource, Datapoints: []Datapoint{a, b}}}}
	fragments := &Set{Logs: []LogRecord{{Body: "b"}, {Body: "a"}}, Metrics: []Metric{{Name: "m", ResourceAttributes: resource, Datapoints: []Datapoint{b}}, {Name: "m", ResourceAttributes: resource, Datapoints: []Datapoint{a}}}}
	Canonicalize(combined)
	Canonicalize(fragments)
	if !reflect.DeepEqual(combined, fragments) {
		t.Fatalf("unstable capture normalization: %#v vs %#v", combined, fragments)
	}
	Canonicalize(fragments)
	if !reflect.DeepEqual(combined, fragments) {
		t.Fatal("not idempotent")
	}
}

func TestSeriesKeysCannotCollideOnDelimiters(t *testing.T) {
	metric := Metric{Datapoints: []Datapoint{
		{Labels: map[string]string{"a": "1,label.b=2"}},
		{Labels: map[string]string{"a": "1", "b": "2"}},
	}}
	if metric.UniqueSeriesCount() != 2 {
		t.Fatal("distinct series collapsed")
	}
}
