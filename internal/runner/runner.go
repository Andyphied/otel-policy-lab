package runner

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/andyphied/otel-policy-lab/internal/telemetry"
)

// RunRequest contains inputs for a pipeline run.
type RunRequest struct {
	FixturePath         string
	CollectorConfigPath string
	Fixture             *telemetry.Set
}

// RunResult contains captured telemetry and run metadata.
type RunResult struct {
	RunnerName            string
	Input                 *telemetry.Set
	Output                *telemetry.Set
	RunnerWarnings        []string
	SimulatedProcessors   []string
	UnsupportedProcessors []string
}

// PipelineRunner executes or simulates a Collector pipeline.
type PipelineRunner interface {
	Run(ctx context.Context, req RunRequest) (*RunResult, error)
}

// New returns a PipelineRunner for the given runner name.
func New(name string) (PipelineRunner, error) {
	switch name {
	case "fixture", "":
		return &FixtureRunner{}, nil
	default:
		return nil, fmt.Errorf("unsupported runner %q (supported: fixture)", name)
	}
}

// FixtureRunner simulates a Collector pipeline using fixture data and config hints.
type FixtureRunner struct{}

// Run applies deterministic transformations based on collector config.
func (r *FixtureRunner) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if req.Fixture == nil {
		return nil, fmt.Errorf("fixture telemetry is required")
	}

	if req.CollectorConfigPath == "" {
		return nil, fmt.Errorf("collector config path is required")
	}

	config, err := os.ReadFile(req.CollectorConfigPath)
	if err != nil {
		return nil, fmt.Errorf("read collector config: %w", err)
	}

	output := req.Fixture.Clone()

	cfg, err := parseCollectorConfig(config)
	if err != nil {
		return nil, fmt.Errorf("parse collector config: %w", err)
	}
	runOutput := applyConfig(output, cfg)
	warnings := append([]string{}, runOutput.warnings...)

	return &RunResult{
		RunnerName:            "fixture",
		Input:                 req.Fixture,
		Output:                runOutput.output,
		RunnerWarnings:        warnings,
		SimulatedProcessors:   runOutput.simulated,
		UnsupportedProcessors: runOutput.unsupported,
	}, nil
}

type collectorConfig struct {
	Processors map[string]map[string]any `yaml:"processors"`
	Service    struct {
		Pipelines map[string]struct {
			Processors []string `yaml:"processors"`
		} `yaml:"pipelines"`
	} `yaml:"service"`
}

func parseCollectorConfig(data []byte) (*collectorConfig, error) {
	var cfg collectorConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

type applyResult struct {
	output      *telemetry.Set
	warnings    []string
	simulated   []string
	unsupported []string
}

func applyConfig(input *telemetry.Set, cfg *collectorConfig) applyResult {
	output := input.Clone()
	result := applyResult{output: output}

	pipelines := cfg.pipelineProcessors()
	for _, signal := range orderedSignals(pipelines) {
		processors := pipelines[signal]
		if signal == "traces" {
			result.warnings = append(result.warnings, "fixture runner does not simulate span dropping or sampling; trace preservation checks are fixture-local")
		}
		for _, name := range processors {
			processor, ok := cfg.Processors[name]
			if !ok {
				result.warnings = append(result.warnings, fmt.Sprintf("collector pipeline references undefined processor %s", name))
				result.unsupported = append(result.unsupported, name)
				continue
			}
			result.applyProcessor(signal, name, processor)
		}
	}
	result.simulated = sortedUnique(result.simulated)
	result.unsupported = sortedUnique(result.unsupported)
	return result
}

func (cfg *collectorConfig) pipelineProcessors() map[string][]string {
	used := make(map[string][]string)
	for name, pipeline := range cfg.Service.Pipelines {
		signal := strings.Split(name, "/")[0]
		used[signal] = append(used[signal], pipeline.Processors...)
	}
	if len(used) > 0 {
		return used
	}
	all := sortedProcessorNames(cfg.Processors)
	return map[string][]string{
		"logs":    all,
		"traces":  all,
		"metrics": all,
	}
}

func (r *applyResult) applyProcessor(signal, name string, processor map[string]any) {
	switch {
	case isNoContentEffectProcessor(name):
		r.simulated = append(r.simulated, name)
	case strings.HasPrefix(name, "attributes/") || name == "attributes":
		if _, hasInclude := processor["include"]; hasInclude {
			r.warnings = append(r.warnings, fmt.Sprintf("fixture runner partially simulated processor %s; include matching is not supported", name))
		}
		if _, hasExclude := processor["exclude"]; hasExclude {
			r.warnings = append(r.warnings, fmt.Sprintf("fixture runner partially simulated processor %s; exclude matching is not supported", name))
		}
		removeKeys := stringSlice(processor["actions"], "key")
		if len(removeKeys) == 0 {
			r.warnings = append(r.warnings, fmt.Sprintf("fixture runner partially simulated processor %s; only attributes delete actions are supported", name))
			r.unsupported = append(r.unsupported, name)
			return
		}
		r.output = removeAttributesForSignal(r.output, signal, removeKeys)
		r.simulated = append(r.simulated, name)
	case strings.HasPrefix(name, "filter/") || name == "filter":
		if signal != "logs" {
			r.warnings = append(r.warnings, fmt.Sprintf("fixture runner did not simulate processor %s for %s pipeline", name, signal))
			r.unsupported = append(r.unsupported, name)
			return
		}
		if r.applyDebugLogFilter(processor) {
			r.simulated = append(r.simulated, name)
			r.warnings = append(r.warnings, fmt.Sprintf("fixture runner partially simulated processor %s; full filter/OTTL semantics are not supported", name))
			return
		}
		r.warnings = append(r.warnings, fmt.Sprintf("fixture runner did not simulate processor %s", name))
		r.unsupported = append(r.unsupported, name)
	default:
		r.warnings = append(r.warnings, fmt.Sprintf("fixture runner did not simulate processor %s", name))
		r.unsupported = append(r.unsupported, name)
	}
}

func (r *applyResult) applyDebugLogFilter(processor map[string]any) bool {
	expr, _ := processor["logs"].(map[string]any)
	if expr == nil {
		return false
	}
	include, ok := expr["include"].(map[string]any)
	if !ok {
		return false
	}
	if matchType, _ := include["match_type"].(string); matchType != "regexp" {
		return false
	}
	exprStr, ok := include["expressions"].([]any)
	if !ok || len(exprStr) == 0 {
		return false
	}
	bodyExpr, _ := exprStr[0].(string)
	if !strings.Contains(bodyExpr, "debug") {
		return false
	}

	before := len(r.output.Logs)
	r.output.Logs = filterDebugLogs(r.output.Logs)
	after := len(r.output.Logs)
	if before > 0 {
		reduction := float64(before-after) / float64(before) * 100
		if reduction >= 50 {
			r.warnings = append(r.warnings, fmt.Sprintf("log volume reduced by %.0f%%; verify debug logs are not over-filtered", reduction))
		}
	}
	return true
}

func stringSlice(actions any, field string) []string {
	items, ok := actions.([]any)
	if !ok {
		return nil
	}
	var keys []string
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if action, _ := m["action"].(string); action != "delete" {
			continue
		}
		if key, _ := m[field].(string); key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

func removeAttributesForSignal(set *telemetry.Set, signal string, keys []string) *telemetry.Set {
	if len(keys) == 0 {
		return set
	}
	keySet := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		keySet[k] = struct{}{}
	}

	switch signal {
	case "logs":
		for i := range set.Logs {
			set.Logs[i].ResourceAttributes = deleteKeys(set.Logs[i].ResourceAttributes, keySet)
			set.Logs[i].Attributes = deleteKeys(set.Logs[i].Attributes, keySet)
		}
	case "traces":
		for i := range set.Spans {
			set.Spans[i].ResourceAttributes = deleteKeys(set.Spans[i].ResourceAttributes, keySet)
			set.Spans[i].Attributes = deleteKeys(set.Spans[i].Attributes, keySet)
		}
	case "metrics":
		for i := range set.Metrics {
			set.Metrics[i].ResourceAttributes = deleteKeys(set.Metrics[i].ResourceAttributes, keySet)
			for j := range set.Metrics[i].Datapoints {
				set.Metrics[i].Datapoints[j].Labels = deleteKeys(set.Metrics[i].Datapoints[j].Labels, keySet)
			}
		}
	}
	return set
}

func deleteKeys(m map[string]string, keys map[string]struct{}) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		if _, forbidden := keys[k]; forbidden {
			continue
		}
		out[k] = v
	}
	return out
}

func filterDebugLogs(logs []telemetry.LogRecord) []telemetry.LogRecord {
	var filtered []telemetry.LogRecord
	for _, log := range logs {
		if strings.Contains(strings.ToLower(log.Body), "debug") {
			continue
		}
		filtered = append(filtered, log)
	}
	return filtered
}

func isNoContentEffectProcessor(name string) bool {
	return name == "batch" || strings.HasPrefix(name, "batch/") || name == "memory_limiter" || strings.HasPrefix(name, "memory_limiter/")
}

func sortedProcessorNames(processors map[string]map[string]any) []string {
	names := make([]string, 0, len(processors))
	for name := range processors {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedUnique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func orderedSignals(pipelines map[string][]string) []string {
	seen := make(map[string]struct{}, len(pipelines))
	var ordered []string
	for _, signal := range []string{"logs", "traces", "metrics"} {
		if _, ok := pipelines[signal]; ok {
			ordered = append(ordered, signal)
			seen[signal] = struct{}{}
		}
	}
	var extras []string
	for signal := range pipelines {
		if _, ok := seen[signal]; !ok {
			extras = append(extras, signal)
		}
	}
	sort.Strings(extras)
	return append(ordered, extras...)
}
