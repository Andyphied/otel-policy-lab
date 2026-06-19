package policy

import (
	"strings"
	"testing"
)

func TestParseValidPolicy(t *testing.T) {
	data := []byte(`
version: 1
assertions:
  logs:
    case_insensitive: true
    forbidden_attributes:
      - password
    forbidden_value_patterns:
      - (?i)bearer
    forbidden_body_patterns:
      - (?i)password=
    required_resource_attributes:
      - service.name
  traces:
    preserve:
      - status.code=="ERROR"
    forbidden_attributes:
      - authorization
    forbidden_value_patterns:
      - (?i)secret
    required_resource_attributes:
      - service.name
  metrics:
    forbidden_labels:
      - user.id
    max_series_per_metric: 10000
    required_resource_attributes:
      - service.name
`)
	p, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if p.Version != 1 {
		t.Fatalf("Version = %d, want 1", p.Version)
	}
	if len(p.Assertions.Logs.ForbiddenAttributes) != 1 {
		t.Fatalf("expected 1 forbidden log attribute")
	}
	if !p.Assertions.Logs.CaseInsensitive {
		t.Fatalf("expected case_insensitive to parse")
	}
	if len(p.Assertions.Logs.ForbiddenValuePatterns) != 1 || len(p.Assertions.Logs.ForbiddenBodyPatterns) != 1 {
		t.Fatalf("expected forbidden value/body patterns to parse")
	}
	if p.Assertions.Metrics.MaxSeriesPerMetric != 10000 {
		t.Fatalf("MaxSeriesPerMetric = %d, want 10000", p.Assertions.Metrics.MaxSeriesPerMetric)
	}
}

func TestParseMissingVersion(t *testing.T) {
	data := []byte(`
assertions:
  logs:
    forbidden_attributes:
      - password
`)
	_, err := Parse(data)
	if err == nil || !strings.Contains(err.Error(), "version is required") {
		t.Fatalf("expected version error, got %v", err)
	}
}

func TestParseUnsupportedPreserveExpression(t *testing.T) {
	data := []byte(`
version: 1
assertions:
  traces:
    preserve:
      - span.kind == "server"
`)
	_, err := Parse(data)
	if err == nil || !strings.Contains(err.Error(), "unsupported trace preserve expression") {
		t.Fatalf("expected preserve expression error, got %v", err)
	}
}

func TestNormalizePreserveExpression(t *testing.T) {
	if got := NormalizePreserveExpression(`status.code=='ERROR'`); got != `status.code == "ERROR"` {
		t.Fatalf("NormalizePreserveExpression() = %q", got)
	}
}

func TestParseNoAssertions(t *testing.T) {
	data := []byte(`
version: 1
assertions: {}
`)
	_, err := Parse(data)
	if err == nil || !strings.Contains(err.Error(), "at least one assertion group") {
		t.Fatalf("expected no assertions error, got %v", err)
	}
}

func TestParseRejectsUnknownSignal(t *testing.T) {
	data := []byte(`
version: 1
assertions:
  logz:
    forbidden_attributes:
      - password
`)
	_, err := Parse(data)
	if err == nil || !strings.Contains(err.Error(), "field logz not found") {
		t.Fatalf("expected unknown signal error, got %v", err)
	}
}

func TestParseRejectsUnknownAssertionKey(t *testing.T) {
	data := []byte(`
version: 1
assertions:
  logs:
    forbidden_attribute:
      - password
`)
	_, err := Parse(data)
	if err == nil || !strings.Contains(err.Error(), "field forbidden_attribute not found") {
		t.Fatalf("expected unknown assertion key error, got %v", err)
	}
}
