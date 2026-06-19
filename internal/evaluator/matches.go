package evaluator

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/andyphied/otel-policy-lab/internal/policy"
	"github.com/andyphied/otel-policy-lab/internal/telemetry"
)

// forbiddenMatch records a policy match without storing sensitive raw values.
type forbiddenMatch struct {
	Kind        string `json:"kind"`
	Key         string `json:"key,omitempty"`
	Pattern     string `json:"pattern,omitempty"`
	ValueLength int    `json:"value_length,omitempty"`
	Index       int    `json:"index"`
	Metric      string `json:"metric,omitempty"`
}

func findForbiddenAttributeMatches(kind string, index int, metric string, attrs map[string]string, forbidden []string, caseInsensitive bool) []forbiddenMatch {
	keys := sortedKeys(attrs)
	var matches []forbiddenMatch
	for _, key := range keys {
		for _, pattern := range forbidden {
			if matchKey(pattern, key, caseInsensitive) {
				matches = append(matches, forbiddenMatch{Kind: kind, Key: key, Pattern: pattern, Index: index, Metric: metric})
			}
		}
	}
	return matches
}

func findForbiddenValueMatches(kind string, index int, metric string, attrs map[string]string, patterns []*regexp.Regexp) []forbiddenMatch {
	keys := sortedKeys(attrs)
	var matches []forbiddenMatch
	for _, key := range keys {
		value := attrs[key]
		for _, pattern := range patterns {
			if pattern.MatchString(value) {
				matches = append(matches, forbiddenMatch{
					Kind:        kind,
					Key:         key,
					Pattern:     pattern.String(),
					ValueLength: len(value),
					Index:       index,
					Metric:      metric,
				})
			}
		}
	}
	return matches
}

func findForbiddenStringMatches(kind string, index int, metric, value string, patterns []*regexp.Regexp) []forbiddenMatch {
	var matches []forbiddenMatch
	for _, pattern := range patterns {
		if pattern.MatchString(value) {
			matches = append(matches, forbiddenMatch{
				Kind:        kind,
				Pattern:     pattern.String(),
				ValueLength: len(value),
				Index:       index,
				Metric:      metric,
			})
		}
	}
	return matches
}

// matchKey compares attribute or label keys. Glob patterns use path.Match where * does not cross "/".
func matchKey(pattern, key string, caseInsensitive bool) bool {
	if caseInsensitive {
		pattern = strings.ToLower(pattern)
		key = strings.ToLower(key)
	}
	if strings.Contains(pattern, "*") {
		ok, err := path.Match(pattern, key)
		return err == nil && ok
	}
	return pattern == key
}

func compilePatterns(patterns []string, caseInsensitive bool) ([]*regexp.Regexp, error) {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		if caseInsensitive && !strings.HasPrefix(pattern, "(?i)") {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid forbidden value/body pattern %q: %w", pattern, err)
		}
		compiled = append(compiled, re)
	}
	return compiled, nil
}

func dedupeForbiddenMatches(matches []forbiddenMatch) []forbiddenMatch {
	seen := make(map[string]struct{}, len(matches))
	out := make([]forbiddenMatch, 0, len(matches))
	for _, match := range matches {
		key := fmt.Sprintf("%s|%s|%s|%d|%s|%d", match.Kind, match.Key, match.Pattern, match.Index, match.Metric, match.ValueLength)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, match)
	}
	return sortForbiddenMatches(out)
}

func sortForbiddenMatches(matches []forbiddenMatch) []forbiddenMatch {
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Kind != matches[j].Kind {
			return matches[i].Kind < matches[j].Kind
		}
		if matches[i].Metric != matches[j].Metric {
			return matches[i].Metric < matches[j].Metric
		}
		if matches[i].Index != matches[j].Index {
			return matches[i].Index < matches[j].Index
		}
		if matches[i].Key != matches[j].Key {
			return matches[i].Key < matches[j].Key
		}
		return matches[i].Pattern < matches[j].Pattern
	})
	return matches
}

func forbiddenMatchDetails(matches []forbiddenMatch) map[string]any {
	return map[string]any{"matches": matches}
}

func summarizeForbiddenMatches(matches []forbiddenMatch) string {
	parts := make([]string, 0, len(matches))
	for _, match := range matches {
		switch match.Kind {
		case "attribute_key", "label_key", "resource_key":
			parts = append(parts, fmt.Sprintf("%s %q matched %q", match.Kind, match.Key, match.Pattern))
		case "attribute_value", "label_value", "resource_value":
			parts = append(parts, fmt.Sprintf("value for %q matched %q", match.Key, match.Pattern))
		case "body":
			parts = append(parts, fmt.Sprintf("log body matched %q", match.Pattern))
		case "span_name":
			parts = append(parts, fmt.Sprintf("span name matched %q", match.Pattern))
		}
	}
	sort.Strings(parts)
	if len(parts) > 3 {
		return strings.Join(parts[:3], "; ") + fmt.Sprintf("; and %d more", len(parts)-3)
	}
	return strings.Join(parts, "; ")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func findForbiddenLogMatches(set *telemetry.Set, assertions *policy.LogsAssertions) ([]forbiddenMatch, error) {
	valuePatterns, err := compilePatterns(assertions.ForbiddenValuePatterns, assertions.CaseInsensitive)
	if err != nil {
		return nil, err
	}
	bodyPatterns, err := compilePatterns(assertions.ForbiddenBodyPatterns, assertions.CaseInsensitive)
	if err != nil {
		return nil, err
	}

	var matches []forbiddenMatch
	for i, log := range set.Logs {
		matches = append(matches, findForbiddenAttributeMatches("resource_key", i, "", log.ResourceAttributes, assertions.ForbiddenAttributes, assertions.CaseInsensitive)...)
		matches = append(matches, findForbiddenValueMatches("resource_value", i, "", log.ResourceAttributes, valuePatterns)...)
		matches = append(matches, findForbiddenAttributeMatches("attribute_key", i, "", log.Attributes, assertions.ForbiddenAttributes, assertions.CaseInsensitive)...)
		matches = append(matches, findForbiddenValueMatches("attribute_value", i, "", log.Attributes, valuePatterns)...)
		matches = append(matches, findForbiddenStringMatches("body", i, "", log.Body, bodyPatterns)...)
	}
	return dedupeForbiddenMatches(matches), nil
}

func findForbiddenSpanMatches(set *telemetry.Set, assertions *policy.TracesAssertions) ([]forbiddenMatch, error) {
	valuePatterns, err := compilePatterns(assertions.ForbiddenValuePatterns, assertions.CaseInsensitive)
	if err != nil {
		return nil, err
	}

	var matches []forbiddenMatch
	for i, span := range set.Spans {
		matches = append(matches, findForbiddenAttributeMatches("resource_key", i, "", span.ResourceAttributes, assertions.ForbiddenAttributes, assertions.CaseInsensitive)...)
		matches = append(matches, findForbiddenValueMatches("resource_value", i, "", span.ResourceAttributes, valuePatterns)...)
		matches = append(matches, findForbiddenAttributeMatches("attribute_key", i, "", span.Attributes, assertions.ForbiddenAttributes, assertions.CaseInsensitive)...)
		matches = append(matches, findForbiddenValueMatches("attribute_value", i, "", span.Attributes, valuePatterns)...)
		matches = append(matches, findForbiddenStringMatches("span_name", i, "", span.Name, valuePatterns)...)
	}
	return dedupeForbiddenMatches(matches), nil
}

func findForbiddenMetricMatches(set *telemetry.Set, assertions *policy.MetricsAssertions) ([]forbiddenMatch, error) {
	valuePatterns, err := compilePatterns(assertions.ForbiddenLabelValuePatterns, assertions.CaseInsensitive)
	if err != nil {
		return nil, err
	}

	var matches []forbiddenMatch
	for mi, metric := range set.Metrics {
		matches = append(matches, findForbiddenAttributeMatches("resource_key", mi, metric.Name, metric.ResourceAttributes, assertions.ForbiddenLabels, assertions.CaseInsensitive)...)
		matches = append(matches, findForbiddenValueMatches("resource_value", mi, metric.Name, metric.ResourceAttributes, valuePatterns)...)
		for i, dp := range metric.Datapoints {
			matches = append(matches, findForbiddenAttributeMatches("label_key", i, metric.Name, dp.Labels, assertions.ForbiddenLabels, assertions.CaseInsensitive)...)
			matches = append(matches, findForbiddenValueMatches("label_value", i, metric.Name, dp.Labels, valuePatterns)...)
		}
	}
	return dedupeForbiddenMatches(matches), nil
}
