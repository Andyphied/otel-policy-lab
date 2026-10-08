package runner

import (
	"strings"
	"testing"
	"time"

	"github.com/andyphied/otel-policy-lab/internal/telemetry"
	"gopkg.in/yaml.v3"
)

const topologySource = `receivers:
  otlp/source:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
processors:
  attributes/second:
    actions:
      - key: token
        value: $${env:TEST_PROCESSOR_VALUE}
        action: insert
  transform/first:
    log_statements:
      - context: log
        statements: ['set(body, "masked")']
  batch/unused: {}
exporters:
  otlp/production:
    endpoint: production.example:4317
    headers: {Authorization: secret-exporter-value}
extensions:
  health_check:
    endpoint: 0.0.0.0:13133
service:
  extensions: [health_check]
  telemetry:
    logs: {level: debug}
  pipelines:
    logs/main:
      receivers: [otlp/source]
      processors: [transform/first, attributes/second]
      exporters: [otlp/production]
`

func emptyRaw(t *testing.T) *telemetry.OTLP {
	t.Helper()
	raw, err := telemetry.ParseOTLP([]byte(`{"resourceLogs": []}`))
	if err != nil {
		t.Fatal(err)
	}
	raw.Logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("test")
	return raw
}

func TestHarnessPreservesOrderedDefinitionsAndExcludesProduction(t *testing.T) {
	top, err := parseTopology([]byte(topologySource), emptyRaw(t))
	if err != nil {
		t.Fatal(err)
	}
	data, err := top.harness("127.0.0.1:12345", "127.0.0.1:12346")
	if err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []string{"production.example", "secret-exporter-value", "health_check", "otlp/source", "otlp/production", "0.0.0.0", "batch/unused", "extensions:"} {
		if strings.Contains(string(data), excluded) {
			t.Fatalf("source component leaked: %s", excluded)
		}
	}
	if !strings.Contains(string(data), "$${env:TEST_PROCESSOR_VALUE}") || !strings.Contains(string(data), `set(body, "masked")`) {
		t.Fatal("processor semantics were rewritten")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	root := doc.Content[0]
	pipeline := mappingValue(mappingValue(mappingValue(root, "service"), "pipelines"), "logs/main")
	refs := mappingValue(pipeline, "processors")
	if len(refs.Content) != 2 || refs.Content[0].Value != "transform/first" || refs.Content[1].Value != "attributes/second" {
		t.Fatal("processor order changed")
	}
	if _, err := top.harness("0.0.0.0:1234", "127.0.0.1:1235"); err == nil {
		t.Fatal("accepted non-loopback endpoint")
	}
}

func TestTopologyRejectsAmbiguousOrUnsupportedInputs(t *testing.T) {
	cases := map[string]string{
		"duplicate mapping":   strings.Replace(topologySource, "processors:\n", "processors: {}\nprocessors:\n", 1),
		"multiple documents":  topologySource + "---\n{}\n",
		"alias":               strings.Replace(topologySource, "actions:\n", "actions: &actions\n", 1),
		"connector":           "connectors: {forward: {}}\n" + topologySource,
		"routing processor":   strings.ReplaceAll(topologySource, "attributes/second", "routing/second"),
		"duplicate signal":    topologySource + "    logs/another: {receivers: [otlp/source], exporters: [otlp/production]}\n",
		"undefined processor": strings.Replace(topologySource, "[transform/first, attributes/second]", "[attributes/missing]", 1),
		"undefined receiver":  strings.Replace(topologySource, "receivers: [otlp/source]", "receivers: [otlp/missing]", 1),
		"undefined exporter":  strings.Replace(topologySource, "exporters: [otlp/production]", "exporters: [otlp/missing]", 1),
		"include list":        strings.Replace(topologySource, "[transform/first, attributes/second]", "${file:chain.yaml}", 1),
		"include definition":  strings.Replace(topologySource, "  attributes/second:\n    actions:\n      - key: token\n        value: $${env:TEST_PROCESSOR_VALUE}\n        action: insert", "  attributes/second: ${file:processor.yaml}", 1),
		"nested provider":     strings.Replace(topologySource, "$${env:TEST_PROCESSOR_VALUE}", "${env:TEST_PROCESSOR_VALUE}", 1),
		"unknown signal":      strings.Replace(topologySource, "logs/main:", "profiles/main:", 1),
		"invalid reference":   strings.Replace(topologySource, "[transform/first, attributes/second]", "[secret value not an identifier]", 1),
		"unknown root":        topologySource + "arbitrary: true\n",
		"nonstring key":       "7: test\n" + topologySource,
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseTopology([]byte(source), emptyRaw(t)); err == nil {
				t.Fatal("accepted unsupported topology")
			}
		})
	}
}

func TestTopologyRequiresPipelineForPopulatedSignal(t *testing.T) {
	raw := emptyRaw(t)
	raw.Traces.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("test")
	if _, err := parseTopology([]byte(topologySource), raw); err == nil || !strings.Contains(err.Error(), "no traces pipeline") {
		t.Fatalf("got %v", err)
	}
}

func TestStatefulDecisionWindow(t *testing.T) {
	source := strings.Replace(topologySource, "  batch/unused: {}", "  tail_sampling:\n    decision_wait: 2s", 1)
	source = strings.Replace(source, "[transform/first, attributes/second]", "[tail_sampling]", 1)
	top, err := parseTopology([]byte(source), emptyRaw(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(top.inconclusive(time.Second)) != 1 {
		t.Fatal("short settling interval should be inconclusive")
	}
	if len(top.inconclusive(3*time.Second)) != 1 {
		t.Fatal("settling alone cannot prove a timing processor reached its decision")
	}
}

func TestMissingComponentHintsExposeOnlyRetainedValidatedIDs(t *testing.T) {
	top, err := parseTopology([]byte(topologySource), emptyRaw(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ logs, want string }{
		{`'processors' unknown type: "transform" for id: "transform/first" (secret-token=value)`, "; processor component transform/first is unavailable"},
		{`'receivers' unknown type: "otlp" for id: "otlp/policy_lab" (secret-token=value)`, "; required harness component otlp/policy_lab is unavailable"},
		{`unknown type: "secret" for id: "secret/password"`, ""},
		{`unknown type: "transform" for id: "transform/first;secret-password"`, ""},
		{`unknown type: "secret" for id: "transform/first"`, ""},
		{`unknown type: "batch" for id: "batch/unused"`, ""},
	} {
		if got := missingComponentHint(test.logs, top); got != test.want {
			t.Fatalf("unsafe or incorrect hint: got %q want %q", got, test.want)
		}
	}
}
