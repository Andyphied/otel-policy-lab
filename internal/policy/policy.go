package policy

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const SupportedVersion = 1

// Policy represents a parsed observability policy file.
type Policy struct {
	Version    int        `yaml:"version"`
	Assertions Assertions `yaml:"assertions"`
}

// Assertions groups signal-specific policy checks.
type Assertions struct {
	Logs    *LogsAssertions    `yaml:"logs,omitempty"`
	Traces  *TracesAssertions  `yaml:"traces,omitempty"`
	Metrics *MetricsAssertions `yaml:"metrics,omitempty"`
}

// LogsAssertions defines log policy checks.
type LogsAssertions struct {
	ForbiddenAttributes        []string `yaml:"forbidden_attributes,omitempty"`
	ForbiddenValuePatterns     []string `yaml:"forbidden_value_patterns,omitempty"`
	ForbiddenBodyPatterns      []string `yaml:"forbidden_body_patterns,omitempty"`
	CaseInsensitive            bool     `yaml:"case_insensitive,omitempty"`
	RequiredResourceAttributes []string `yaml:"required_resource_attributes,omitempty"`
}

// TracesAssertions defines trace policy checks.
type TracesAssertions struct {
	Preserve                   []string `yaml:"preserve,omitempty"`
	ForbiddenAttributes        []string `yaml:"forbidden_attributes,omitempty"`
	ForbiddenValuePatterns     []string `yaml:"forbidden_value_patterns,omitempty"`
	CaseInsensitive            bool     `yaml:"case_insensitive,omitempty"`
	RequiredResourceAttributes []string `yaml:"required_resource_attributes,omitempty"`
}

// MetricsAssertions defines metric policy checks.
type MetricsAssertions struct {
	ForbiddenLabels             []string `yaml:"forbidden_labels,omitempty"`
	ForbiddenLabelValuePatterns []string `yaml:"forbidden_label_value_patterns,omitempty"`
	CaseInsensitive             bool     `yaml:"case_insensitive,omitempty"`
	MaxSeriesPerMetric         int      `yaml:"max_series_per_metric,omitempty"`
	RequiredResourceAttributes []string `yaml:"required_resource_attributes,omitempty"`
}

// Load reads and validates a policy file from disk.
func Load(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read policy file: %w", err)
	}
	return Parse(data)
}

// Parse unmarshals and validates policy YAML content.
func Parse(data []byte) (*Policy, error) {
	var p Policy
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&p); err != nil {
		return nil, fmt.Errorf("parse policy yaml: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Validate checks policy structure and supported fields.
func (p *Policy) Validate() error {
	if p.Version == 0 {
		return fmt.Errorf("policy version is required")
	}
	if p.Version != SupportedVersion {
		return fmt.Errorf("unsupported policy version %d; expected %d", p.Version, SupportedVersion)
	}
	if !p.hasAssertions() {
		return fmt.Errorf("policy must define at least one assertion group")
	}
	for _, expr := range p.Assertions.Traces.PreserveExprs() {
		if NormalizePreserveExpression(expr) != `status.code == "ERROR"` {
			return fmt.Errorf("unsupported trace preserve expression: %q (MVP supports only status.code == \"ERROR\")", expr)
		}
	}
	return nil
}

// NormalizePreserveExpression accepts harmless spacing/quote variants for the
// single preserve predicate supported by the MVP.
func NormalizePreserveExpression(expr string) string {
	normalized := strings.ReplaceAll(expr, " ", "")
	normalized = strings.ReplaceAll(normalized, "'", `"`)
	if normalized == `status.code=="ERROR"` {
		return `status.code == "ERROR"`
	}
	return expr
}

func (p *Policy) hasAssertions() bool {
	if p.Assertions.Logs != nil && p.Assertions.Logs.hasChecks() {
		return true
	}
	if p.Assertions.Traces != nil && p.Assertions.Traces.hasChecks() {
		return true
	}
	if p.Assertions.Metrics != nil && p.Assertions.Metrics.hasChecks() {
		return true
	}
	return false
}

func (l *LogsAssertions) hasChecks() bool {
	if l == nil {
		return false
	}
	return len(l.ForbiddenAttributes) > 0 ||
		len(l.ForbiddenValuePatterns) > 0 ||
		len(l.ForbiddenBodyPatterns) > 0 ||
		len(l.RequiredResourceAttributes) > 0
}

func (t *TracesAssertions) hasChecks() bool {
	if t == nil {
		return false
	}
	return len(t.Preserve) > 0 ||
		len(t.ForbiddenAttributes) > 0 ||
		len(t.ForbiddenValuePatterns) > 0 ||
		len(t.RequiredResourceAttributes) > 0
}

func (t *TracesAssertions) PreserveExprs() []string {
	if t == nil {
		return nil
	}
	return t.Preserve
}

func (m *MetricsAssertions) hasChecks() bool {
	if m == nil {
		return false
	}
	return len(m.ForbiddenLabels) > 0 ||
		len(m.ForbiddenLabelValuePatterns) > 0 ||
		m.MaxSeriesPerMetric > 0 ||
		len(m.RequiredResourceAttributes) > 0
}
