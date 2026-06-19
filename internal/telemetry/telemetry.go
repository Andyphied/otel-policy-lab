package telemetry

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// Set holds normalized telemetry extracted from OTLP JSON fixtures.
type Set struct {
	Logs    []LogRecord
	Spans   []Span
	Metrics []Metric
}

// LogRecord represents a single log record with resource context.
type LogRecord struct {
	ResourceAttributes map[string]string
	Attributes         map[string]string
	Body               string
}

// Span represents a single span with resource context.
type Span struct {
	TraceID            string
	SpanID             string
	Name               string
	StatusCode         string
	ResourceAttributes map[string]string
	Attributes         map[string]string
}

// Metric represents a metric with datapoints and labels.
type Metric struct {
	Name               string
	ResourceAttributes map[string]string
	Datapoints         []Datapoint
}

// Datapoint represents one metric series observation.
type Datapoint struct {
	Labels map[string]string
}

// Summary provides aggregate counts for reporting.
type Summary struct {
	InputLogCount     int
	OutputLogCount    int
	InputSpanCount    int
	OutputSpanCount   int
	InputMetricCount  int
	OutputMetricCount int
	InputSeriesCount  int
	OutputSeriesCount int
}

// Load reads and parses an OTLP JSON fixture from disk.
func Load(path string) (*Set, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fixture file: %w", err)
	}
	return Parse(data)
}

// Parse unmarshals OTLP JSON fixture content into a normalized Set.
func Parse(data []byte) (*Set, error) {
	raw, err := parseOTLPJSON(data)
	if err != nil {
		return nil, fmt.Errorf("parse fixture json: %w", err)
	}
	return raw.normalize(), nil
}

// SummaryStats compares input and output telemetry sets.
func SummaryStats(input, output *Set) Summary {
	return Summary{
		InputLogCount:     len(input.Logs),
		OutputLogCount:    len(output.Logs),
		InputSpanCount:    len(input.Spans),
		OutputSpanCount:   len(output.Spans),
		InputMetricCount:  len(input.Metrics),
		OutputMetricCount: len(output.Metrics),
		InputSeriesCount:  countSeries(input),
		OutputSeriesCount: countSeries(output),
	}
}

func countSeries(set *Set) int {
	seen := make(map[string]struct{})
	for _, m := range set.Metrics {
		for _, series := range m.UniqueSeriesKeys() {
			seen[m.Name+"|"+series] = struct{}{}
		}
	}
	return len(seen)
}

// UniqueSeriesCount returns the number of unique series for a metric.
func (m Metric) UniqueSeriesCount() int {
	return len(m.UniqueSeriesKeys())
}

// UniqueSeriesKeys returns stable series identifiers for this metric.
func (m Metric) UniqueSeriesKeys() []string {
	seen := make(map[string]struct{})
	for _, dp := range m.Datapoints {
		seen[seriesKey(m.ResourceAttributes, dp.Labels)] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func seriesKey(resourceAttrs, labels map[string]string) string {
	parts := make([]string, 0, len(resourceAttrs)+len(labels))
	for k, v := range resourceAttrs {
		parts = append(parts, "resource."+k+"="+v)
	}
	for k, v := range labels {
		parts = append(parts, "label."+k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// Clone returns a deep copy of the telemetry set.
func (s *Set) Clone() *Set {
	if s == nil {
		return &Set{}
	}
	out := &Set{
		Logs:    make([]LogRecord, len(s.Logs)),
		Spans:   make([]Span, len(s.Spans)),
		Metrics: make([]Metric, len(s.Metrics)),
	}
	for i, log := range s.Logs {
		out.Logs[i] = LogRecord{
			ResourceAttributes: cloneMap(log.ResourceAttributes),
			Attributes:         cloneMap(log.Attributes),
			Body:               log.Body,
		}
	}
	for i, span := range s.Spans {
		out.Spans[i] = Span{
			TraceID:            span.TraceID,
			SpanID:             span.SpanID,
			Name:               span.Name,
			StatusCode:         span.StatusCode,
			ResourceAttributes: cloneMap(span.ResourceAttributes),
			Attributes:         cloneMap(span.Attributes),
		}
	}
	for i, metric := range s.Metrics {
		dps := make([]Datapoint, len(metric.Datapoints))
		for j, dp := range metric.Datapoints {
			dps[j] = Datapoint{Labels: cloneMap(dp.Labels)}
		}
		out.Metrics[i] = Metric{
			Name:               metric.Name,
			ResourceAttributes: cloneMap(metric.ResourceAttributes),
			Datapoints:         dps,
		}
	}
	return out
}

func cloneMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
