package evaluator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/andyphied/otel-policy-lab/internal/policy"
	"github.com/andyphied/otel-policy-lab/internal/report"
	"github.com/andyphied/otel-policy-lab/internal/telemetry"
)

// Evaluate runs policy checks against input and output telemetry.
func Evaluate(p *policy.Policy, input, output *telemetry.Set, runnerWarnings []string) []report.CheckResult {
	return EvaluateWithOptions(p, input, output, runnerWarnings, Options{})
}

// Options controls evidence requirements independently of execution strategy.
type Options struct {
	RequireApplicableInput  bool
	UnreliableOutputReasons []string
	UnreliableSignals       map[string][]string
}

// EvaluateWithOptions evaluates assertions with explicit evidence requirements.
func EvaluateWithOptions(p *policy.Policy, input, output *telemetry.Set, runnerWarnings []string, options Options) []report.CheckResult {
	var results []report.CheckResult

	if p.Assertions.Logs != nil {
		results = append(results, evalLogs(p.Assertions.Logs, output)...)
	}
	if p.Assertions.Traces != nil {
		results = append(results, evalTraces(p.Assertions.Traces, input, output, runnerWarnings)...)
	}
	if p.Assertions.Metrics != nil {
		results = append(results, evalMetrics(p.Assertions.Metrics, output)...)
	}
	for i := range results {
		reason := ""
		if options.RequireApplicableInput {
			switch results[i].Signal {
			case "logs":
				if len(input.Logs) == 0 {
					reason = "no logs in fixture"
				}
			case "traces":
				if len(input.Spans) == 0 {
					reason = "no spans in fixture"
				}
				if results[i].Check == "preserve_error_traces" && len(errorSpans(input)) == 0 {
					reason = "no error spans in fixture"
				}
			case "metrics":
				points := 0
				for _, m := range input.Metrics {
					points += len(m.Datapoints)
				}
				if points == 0 {
					reason = "no metric datapoints in fixture"
				}
			}
		}
		if len(options.UnreliableOutputReasons) > 0 {
			reason = strings.Join(options.UnreliableOutputReasons, "; ")
		}
		if scoped := options.UnreliableSignals[results[i].Signal]; len(scoped) > 0 {
			reason = strings.Join(scoped, "; ")
		}
		if reason != "" && !conclusiveObservation(results[i], input) {
			results[i].Status = report.StatusInconclusive
			results[i].Message = fmt.Sprintf("%s cannot be evaluated reliably: %s", results[i].Check, reason)
			results[i].Details = nil
		}
	}

	for _, warning := range runnerWarnings {
		results = append(results, report.CheckResult{
			Status:  report.StatusWarn,
			Signal:  "pipeline",
			Check:   "runner_warning",
			Message: warning,
		})
	}

	return results
}

// Incomplete capture cannot disprove an observed violation. Conversely, missing
// records can be an unfinished processor decision rather than a policy failure.
func conclusiveObservation(check report.CheckResult, input *telemetry.Set) bool {
	if check.Check == "preserve_error_traces" && check.Status == report.StatusPass && len(errorSpans(input)) > 0 {
		return true // Every requested input identity was actually captured.
	}
	if check.Status != report.StatusFail {
		return false
	}
	switch check.Check {
	case "forbidden_data", "forbidden_labels", "max_series_per_metric":
		return true
	case "required_resource_attributes":
		total, _ := check.Details["total_records"].(int)
		return total > 0
	}
	return false
}

func evalLogs(assertions *policy.LogsAssertions, output *telemetry.Set) []report.CheckResult {
	var results []report.CheckResult

	if len(assertions.ForbiddenAttributes) > 0 || len(assertions.ForbiddenValuePatterns) > 0 || len(assertions.ForbiddenBodyPatterns) > 0 {
		matches, err := findForbiddenLogMatches(output, assertions)
		if err != nil {
			results = append(results, fail("logs", "forbidden_data", err.Error(), nil))
		} else if len(matches) == 0 {
			results = append(results, pass("logs", "forbidden_data", "no forbidden log resource attributes, attributes, values, or bodies exported"))
		} else {
			results = append(results, fail("logs", "forbidden_data",
				fmt.Sprintf("forbidden log data exported: %s", summarizeForbiddenMatches(matches)),
				forbiddenMatchDetails(matches)))
		}
	}

	if len(assertions.RequiredResourceAttributes) > 0 {
		check := checkRequiredLogResourceAttributes(output, assertions.RequiredResourceAttributes)
		if !check.failed {
			msg := fmt.Sprintf("all logs include %s", strings.Join(assertions.RequiredResourceAttributes, ", "))
			results = append(results, pass("logs", "required_resource_attributes", msg))
		} else {
			results = append(results, fail("logs", "required_resource_attributes",
				check.message("logs"),
				check.details()))
		}
	}

	return results
}

func evalTraces(assertions *policy.TracesAssertions, input, output *telemetry.Set, runnerWarnings []string) []report.CheckResult {
	var results []report.CheckResult

	for _, expr := range assertions.Preserve {
		if policy.NormalizePreserveExpression(expr) == `status.code == "ERROR"` {
			results = append(results, evalErrorTracePreservation(input, output, runnerWarnings))
		}
	}

	if len(assertions.ForbiddenAttributes) > 0 || len(assertions.ForbiddenValuePatterns) > 0 {
		matches, err := findForbiddenSpanMatches(output, assertions)
		if err != nil {
			results = append(results, fail("traces", "forbidden_data", err.Error(), nil))
		} else if len(matches) == 0 {
			results = append(results, pass("traces", "forbidden_data", "no forbidden span resource attributes, attributes, values, or names exported"))
		} else {
			results = append(results, fail("traces", "forbidden_data",
				fmt.Sprintf("forbidden span data exported: %s", summarizeForbiddenMatches(matches)),
				forbiddenMatchDetails(matches)))
		}
	}

	if len(assertions.RequiredResourceAttributes) > 0 {
		check := checkRequiredSpanResourceAttributes(output, assertions.RequiredResourceAttributes)
		if !check.failed {
			msg := fmt.Sprintf("all traces include %s", strings.Join(assertions.RequiredResourceAttributes, ", "))
			results = append(results, pass("traces", "required_resource_attributes", msg))
		} else {
			results = append(results, fail("traces", "required_resource_attributes",
				check.message("traces"),
				check.details()))
		}
	}

	return results
}

func evalMetrics(assertions *policy.MetricsAssertions, output *telemetry.Set) []report.CheckResult {
	var results []report.CheckResult

	if len(assertions.ForbiddenLabels) > 0 || len(assertions.ForbiddenLabelValuePatterns) > 0 {
		matches, err := findForbiddenMetricMatches(output, assertions)
		if err != nil {
			results = append(results, fail("metrics", "forbidden_labels", err.Error(), nil))
		} else if len(matches) == 0 {
			results = append(results, pass("metrics", "forbidden_labels", "no forbidden metric resource attributes, labels, or label values exported"))
		} else {
			results = append(results, fail("metrics", "forbidden_labels",
				fmt.Sprintf("forbidden metric labels exported: %s", summarizeForbiddenMatches(matches)),
				forbiddenMatchDetails(matches)))
		}
	}

	if assertions.MaxSeriesPerMetric > 0 {
		results = append(results, evalMaxSeries(output, assertions.MaxSeriesPerMetric)...)
	}

	if len(assertions.RequiredResourceAttributes) > 0 {
		check := checkRequiredMetricResourceAttributes(output, assertions.RequiredResourceAttributes)
		if !check.failed {
			msg := fmt.Sprintf("all metrics include %s", strings.Join(assertions.RequiredResourceAttributes, ", "))
			results = append(results, pass("metrics", "required_resource_attributes", msg))
		} else {
			results = append(results, fail("metrics", "required_resource_attributes",
				check.message("metrics"),
				check.details()))
		}
	}

	return results
}

func evalErrorTracePreservation(input, output *telemetry.Set, runnerWarnings []string) report.CheckResult {
	inputErrors := errorSpans(input)
	outputErrors := errorSpans(output)

	if len(inputErrors) == 0 {
		return pass("traces", "preserve_error_traces", "no error traces in fixture; preservation check skipped")
	}

	preserved := 0
	for _, in := range inputErrors {
		if _, ok := outputErrors[spanKey(in)]; ok {
			preserved++
		}
	}

	pct := float64(preserved) / float64(len(inputErrors)) * 100
	if preserved == len(inputErrors) {
		msg := fmt.Sprintf("%.0f%% of error traces preserved", pct)
		if isFixtureLocalTraceCheck(runnerWarnings) {
			msg += " (fixture-local; runner does not drop spans)"
		}
		return pass("traces", "preserve_error_traces", msg)
	}
	return fail("traces", "preserve_error_traces",
		fmt.Sprintf("%.0f%% of error traces preserved (%d/%d)", pct, preserved, len(inputErrors)),
		map[string]any{
			"preserved": preserved,
			"total":     len(inputErrors),
			"percent":   pct,
		})
}

func evalMaxSeries(output *telemetry.Set, limit int) []report.CheckResult {
	var results []report.CheckResult
	violations := 0
	// OTLP exporters may split one metric over resources and requests. The
	// policy limit is per metric name, not per export request or resource block.
	seriesByName := make(map[string]map[string]struct{})
	pointCounts := make(map[string]int)
	for _, metric := range output.Metrics {
		if seriesByName[metric.Name] == nil {
			seriesByName[metric.Name] = make(map[string]struct{})
		}
		for _, key := range metric.UniqueSeriesKeys() {
			seriesByName[metric.Name][key] = struct{}{}
		}
		pointCounts[metric.Name] += len(metric.Datapoints)
	}
	names := make([]string, 0, len(seriesByName))
	for name := range seriesByName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		series := len(seriesByName[name])
		if series > limit {
			violations++
			results = append(results, fail("metrics", "max_series_per_metric",
				fmt.Sprintf("metric %s has %s unique series; limit is %s", name, report.FormatSeries(series), report.FormatSeries(limit)),
				map[string]any{
					"metric":              name,
					"unique_series_count": series,
					"datapoint_count":     pointCounts[name],
					"limit":               limit,
				}))
		}
	}
	if violations == 0 {
		results = append(results, pass("metrics", "max_series_per_metric",
			fmt.Sprintf("all metrics within %s series limit", report.FormatSeries(limit))))
	}
	return results
}

func errorSpans(set *telemetry.Set) map[string]telemetry.Span {
	out := make(map[string]telemetry.Span)
	for _, span := range set.Spans {
		if strings.ToUpper(span.StatusCode) == "ERROR" {
			out[spanKey(span)] = span
		}
	}
	return out
}

func spanKey(span telemetry.Span) string {
	return span.TraceID + ":" + span.SpanID
}

func isFixtureLocalTraceCheck(warnings []string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, "trace preservation checks are fixture-local") {
			return true
		}
	}
	return false
}

type requiredResourceCheck struct {
	failed        bool
	totalRecords  int
	missingCounts map[string]int
	examples      []map[string]any
}

func checkRequiredLogResourceAttributes(set *telemetry.Set, required []string) requiredResourceCheck {
	attrs := make([]map[string]string, 0, len(set.Logs))
	for _, log := range set.Logs {
		attrs = append(attrs, log.ResourceAttributes)
	}
	return checkRequiredResourceAttributes(required, attrs)
}

func checkRequiredSpanResourceAttributes(set *telemetry.Set, required []string) requiredResourceCheck {
	attrs := make([]map[string]string, 0, len(set.Spans))
	for _, span := range set.Spans {
		attrs = append(attrs, span.ResourceAttributes)
	}
	return checkRequiredResourceAttributes(required, attrs)
}

func checkRequiredMetricResourceAttributes(set *telemetry.Set, required []string) requiredResourceCheck {
	attrs := make([]map[string]string, 0, len(set.Metrics))
	for _, metric := range set.Metrics {
		attrs = append(attrs, metric.ResourceAttributes)
	}
	return checkRequiredResourceAttributes(required, attrs)
}

func checkRequiredResourceAttributes(required []string, records []map[string]string) requiredResourceCheck {
	check := requiredResourceCheck{
		totalRecords:  len(records),
		missingCounts: make(map[string]int),
		examples:      []map[string]any{},
	}
	if len(records) == 0 {
		for _, req := range required {
			check.missingCounts[req] = 0
		}
		check.failed = true
		return check
	}

	for i, attrs := range records {
		var missingForRecord []string
		for _, req := range required {
			if _, ok := attrs[req]; !ok {
				check.missingCounts[req]++
				missingForRecord = append(missingForRecord, req)
			}
		}
		if len(missingForRecord) > 0 {
			check.failed = true
			if len(check.examples) < 3 {
				check.examples = append(check.examples, map[string]any{
					"index":   i,
					"missing": missingForRecord,
				})
			}
		}
	}
	return check
}

func (c requiredResourceCheck) message(signal string) string {
	if c.totalRecords == 0 {
		return fmt.Sprintf("no %s available to verify required resource attributes: %s", signal, strings.Join(sortedMissingKeys(c.missingCounts), ", "))
	}
	parts := make([]string, 0, len(c.missingCounts))
	for _, key := range sortedMissingKeys(c.missingCounts) {
		count := c.missingCounts[key]
		if count > 0 {
			parts = append(parts, fmt.Sprintf("%s (%d/%d records)", key, count, c.totalRecords))
		}
	}
	return fmt.Sprintf("%s missing required resource attributes: %s", signal, strings.Join(parts, ", "))
}

func (c requiredResourceCheck) details() map[string]any {
	return map[string]any{
		"total_records":   c.totalRecords,
		"missing_counts":  c.missingCounts,
		"example_records": c.examples,
	}
}

func sortedMissingKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func pass(signal, check, message string) report.CheckResult {
	return report.CheckResult{Status: report.StatusPass, Signal: signal, Check: check, Message: message}
}

func fail(signal, check, message string, details map[string]any) report.CheckResult {
	return report.CheckResult{Status: report.StatusFail, Signal: signal, Check: check, Message: message, Details: details}
}
