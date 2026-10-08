package telemetry

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// OTLP retains the complete fixture, including fields not used by policies.
// Real processors must receive this data rather than reconstructed Set records.
type OTLP struct {
	Logs    plog.Logs
	Traces  ptrace.Traces
	Metrics pmetric.Metrics
}

// LoadOTLP reads a full-fidelity OTLP JSON fixture.
func LoadOTLP(path string) (*OTLP, error) {
	const maxJSONBytes = 64 << 20
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxJSONBytes {
		return nil, fmt.Errorf("fixture must be a regular file no larger than 64 MiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open OTLP fixture")
	}
	defer func() { _ = f.Close() }()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("fixture must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxJSONBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read fixture file: %w", err)
	}
	if len(data) > maxJSONBytes {
		return nil, fmt.Errorf("fixture exceeds the 64 MiB JSON limit")
	}
	return ParseOTLP(data)
}

// ParseOTLP parses the three standard OTLP JSON signal envelopes.
func ParseOTLP(data []byte) (*OTLP, error) {
	return parseOTLPJSON(data)
}

func parseOTLPJSON(data []byte) (*OTLP, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	out := &OTLP{
		Logs:    plog.NewLogs(),
		Traces:  ptrace.NewTraces(),
		Metrics: pmetric.NewMetrics(),
	}
	if _, ok := raw["resourceLogs"]; ok {
		logs, err := new(plog.JSONUnmarshaler).UnmarshalLogs(onlySignal(raw, "resourceLogs"))
		if err != nil {
			return nil, fmt.Errorf("parse resourceLogs: %w", err)
		}
		out.Logs = logs
	}
	if _, ok := raw["resourceSpans"]; ok {
		traces, err := new(ptrace.JSONUnmarshaler).UnmarshalTraces(onlySignal(raw, "resourceSpans"))
		if err != nil {
			return nil, fmt.Errorf("parse resourceSpans: %w", err)
		}
		out.Traces = traces
	}
	if _, ok := raw["resourceMetrics"]; ok {
		metrics, err := new(pmetric.JSONUnmarshaler).UnmarshalMetrics(onlySignal(raw, "resourceMetrics"))
		if err != nil {
			return nil, fmt.Errorf("parse resourceMetrics: %w", err)
		}
		out.Metrics = metrics
	}
	return out, nil
}

func onlySignal(raw map[string]json.RawMessage, key string) []byte {
	data, _ := json.Marshal(map[string]json.RawMessage{key: raw[key]})
	return data
}

// Normalize produces the policy view without modifying the original pdata.
func (o *OTLP) Normalize() *Set {
	set := &Set{}
	normalizeLogs(o.Logs, set)
	normalizeSpans(o.Traces, set)
	normalizeMetrics(o.Metrics, set)
	return set
}

func (o *OTLP) normalize() *Set { return o.Normalize() }

func normalizeLogs(logs plog.Logs, set *Set) {
	resourceLogs := logs.ResourceLogs()
	for i := 0; i < resourceLogs.Len(); i++ {
		rl := resourceLogs.At(i)
		resourceAttrs := attrsToMap(rl.Resource().Attributes())
		scopeLogs := rl.ScopeLogs()
		for j := 0; j < scopeLogs.Len(); j++ {
			logRecords := scopeLogs.At(j).LogRecords()
			for k := 0; k < logRecords.Len(); k++ {
				lr := logRecords.At(k)
				set.Logs = append(set.Logs, LogRecord{
					ResourceAttributes: cloneMap(resourceAttrs),
					Attributes:         attrsToMap(lr.Attributes()),
					Body:               valueToString(lr.Body()),
				})
			}
		}
	}
}

func normalizeSpans(traces ptrace.Traces, set *Set) {
	resourceSpans := traces.ResourceSpans()
	for i := 0; i < resourceSpans.Len(); i++ {
		rs := resourceSpans.At(i)
		resourceAttrs := attrsToMap(rs.Resource().Attributes())
		scopeSpans := rs.ScopeSpans()
		for j := 0; j < scopeSpans.Len(); j++ {
			spans := scopeSpans.At(j).Spans()
			for k := 0; k < spans.Len(); k++ {
				sp := spans.At(k)
				set.Spans = append(set.Spans, Span{
					TraceID:            sp.TraceID().String(),
					SpanID:             sp.SpanID().String(),
					Name:               sp.Name(),
					StatusCode:         statusCode(sp.Status().Code()),
					ResourceAttributes: cloneMap(resourceAttrs),
					Attributes:         attrsToMap(sp.Attributes()),
				})
			}
		}
	}
}

func normalizeMetrics(metrics pmetric.Metrics, set *Set) {
	resourceMetrics := metrics.ResourceMetrics()
	for i := 0; i < resourceMetrics.Len(); i++ {
		rm := resourceMetrics.At(i)
		resourceAttrs := attrsToMap(rm.Resource().Attributes())
		scopeMetrics := rm.ScopeMetrics()
		for j := 0; j < scopeMetrics.Len(); j++ {
			metricList := scopeMetrics.At(j).Metrics()
			for k := 0; k < metricList.Len(); k++ {
				m := metricList.At(k)
				metric := Metric{
					Name:               m.Name(),
					ResourceAttributes: cloneMap(resourceAttrs),
				}
				appendDatapoints(&metric, m)
				set.Metrics = append(set.Metrics, metric)
			}
		}
	}
}

func attrsToMap(attrs pcommon.Map) map[string]string {
	out := make(map[string]string, attrs.Len())
	attrs.Range(func(k string, v pcommon.Value) bool {
		out[k] = valueToString(v)
		return true
	})
	return out
}

func valueToString(v pcommon.Value) string {
	raw := v.AsRaw()
	data, err := json.Marshal(raw)
	if err != nil {
		return fmt.Sprint(raw)
	}
	if str, ok := raw.(string); ok {
		return str
	}
	return string(data)
}

func statusCode(code ptrace.StatusCode) string {
	switch code {
	case ptrace.StatusCodeOk:
		return "OK"
	case ptrace.StatusCodeError:
		return "ERROR"
	default:
		return ""
	}
}

func appendDatapoints(metric *Metric, m pmetric.Metric) {
	switch m.Type() {
	case pmetric.MetricTypeGauge:
		points := m.Gauge().DataPoints()
		for i := 0; i < points.Len(); i++ {
			metric.Datapoints = append(metric.Datapoints, Datapoint{Labels: attrsToMap(points.At(i).Attributes())})
		}
	case pmetric.MetricTypeSum:
		points := m.Sum().DataPoints()
		for i := 0; i < points.Len(); i++ {
			metric.Datapoints = append(metric.Datapoints, Datapoint{Labels: attrsToMap(points.At(i).Attributes())})
		}
	case pmetric.MetricTypeHistogram:
		points := m.Histogram().DataPoints()
		for i := 0; i < points.Len(); i++ {
			metric.Datapoints = append(metric.Datapoints, Datapoint{Labels: attrsToMap(points.At(i).Attributes())})
		}
	case pmetric.MetricTypeExponentialHistogram:
		points := m.ExponentialHistogram().DataPoints()
		for i := 0; i < points.Len(); i++ {
			metric.Datapoints = append(metric.Datapoints, Datapoint{Labels: attrsToMap(points.At(i).Attributes())})
		}
	case pmetric.MetricTypeSummary:
		points := m.Summary().DataPoints()
		for i := 0; i < points.Len(); i++ {
			metric.Datapoints = append(metric.Datapoints, Datapoint{Labels: attrsToMap(points.At(i).Attributes())})
		}
	}
}
