package runner

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/andyphied/otel-policy-lab/internal/telemetry"
)

const maxConfigBytes = 4 << 20

var componentID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}(/[A-Za-z0-9][A-Za-z0-9_.-]{0,127})?$`)

// PipelineDiagnostics identifies the actual processor chain executed by the harness.
type PipelineDiagnostics struct {
	Name       string   `json:"name"`
	Signal     string   `json:"signal"`
	Processors []string `json:"processors"`
}

type topology struct {
	pipelines  []PipelineDiagnostics
	processors *yaml.Node
}

func parseTopology(data []byte, raw *telemetry.OTLP) (*topology, error) {
	if len(data) > maxConfigBytes {
		return nil, fmt.Errorf("collector configuration exceeds the 4 MiB limit")
	}
	var doc yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("collector configuration is not valid YAML")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("collector configuration must contain exactly one YAML document")
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("collector configuration must be a mapping")
	}
	if err := validateYAML(doc.Content[0], 0); err != nil {
		return nil, err
	}
	root := doc.Content[0]
	for i := 0; i < len(root.Content); i += 2 {
		switch root.Content[i].Value {
		case "receivers", "processors", "exporters", "extensions", "connectors", "service":
		default:
			return nil, fmt.Errorf("collector configuration has an unsupported top-level field")
		}
	}
	components := make(map[string]map[string]*yaml.Node)
	for _, kind := range []string{"receivers", "processors", "exporters", "connectors", "extensions"} {
		items := make(map[string]*yaml.Node)
		section := mappingValue(root, kind)
		if section != nil && section.Tag != "!!null" {
			if section.Kind != yaml.MappingNode {
				return nil, fmt.Errorf("%s must be an explicit mapping; structural provider substitutions are unsupported", kind)
			}
			for i := 0; i < len(section.Content); i += 2 {
				id := section.Content[i].Value
				if !componentID.MatchString(id) {
					return nil, fmt.Errorf("%s contains an invalid component identifier", kind)
				}
				items[id] = section.Content[i+1]
			}
		}
		components[kind] = items
	}
	if len(components["connectors"]) != 0 {
		return nil, fmt.Errorf("connectors are unsupported by the Collector runner")
	}
	service := mappingValue(root, "service")
	if service == nil || service.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("service must be an explicit mapping")
	}
	pipelines := mappingValue(service, "pipelines")
	if pipelines == nil || pipelines.Kind != yaml.MappingNode || len(pipelines.Content) == 0 {
		return nil, fmt.Errorf("service.pipelines must be a nonempty explicit mapping")
	}
	out := &topology{processors: mapNode()}
	seenSignals, seenProcessors := map[string]bool{}, map[string]bool{}
	for i := 0; i < len(pipelines.Content); i += 2 {
		name, pipeline := pipelines.Content[i].Value, pipelines.Content[i+1]
		if !componentID.MatchString(name) {
			return nil, fmt.Errorf("pipeline has an invalid identifier")
		}
		signal := strings.SplitN(name, "/", 2)[0]
		if signal != "logs" && signal != "traces" && signal != "metrics" {
			return nil, fmt.Errorf("pipeline %s has an unsupported signal", name)
		}
		if seenSignals[signal] {
			return nil, fmt.Errorf("multiple %s pipelines are unsupported", signal)
		}
		seenSignals[signal] = true
		if pipeline.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("pipeline %s must be an explicit mapping", name)
		}
		for j := 0; j < len(pipeline.Content); j += 2 {
			switch pipeline.Content[j].Value {
			case "receivers", "processors", "exporters":
			default:
				return nil, fmt.Errorf("pipeline %s has an unsupported field", name)
			}
		}
		var processorIDs []string
		for _, kind := range []string{"receivers", "processors", "exporters"} {
			ids, err := componentSequence(mappingValue(pipeline, kind), kind == "processors")
			if err != nil {
				return nil, fmt.Errorf("pipeline %s %s: %w", name, kind, err)
			}
			for _, id := range ids {
				definition, exists := components[kind][id]
				if !exists {
					return nil, fmt.Errorf("pipeline %s references undefined %s component %s", name, kind, id)
				}
				if kind == "processors" {
					if strings.SplitN(id, "/", 2)[0] == "routing" {
						return nil, fmt.Errorf("routing processors are unsupported by the isolated Collector harness")
					}
					if definition.Kind != yaml.MappingNode && definition.Tag != "!!null" {
						return nil, fmt.Errorf("processor %s must have an explicit mapping; structural provider substitutions are unsupported", id)
					}
					if !seenProcessors[id] {
						out.processors.Content = append(out.processors.Content, strNode(id), cloneNode(definition))
						seenProcessors[id] = true
					}
				}
			}
			if kind == "processors" {
				processorIDs = ids
			}
		}
		out.pipelines = append(out.pipelines, PipelineDiagnostics{Name: name, Signal: signal, Processors: processorIDs})
	}
	for signal, populated := range map[string]bool{"logs": raw.Logs.LogRecordCount() > 0, "traces": raw.Traces.SpanCount() > 0, "metrics": raw.Metrics.MetricCount() > 0} {
		if populated && !seenSignals[signal] {
			return nil, fmt.Errorf("fixture contains %s but no %s pipeline exists", signal, signal)
		}
	}
	sort.Slice(out.pipelines, func(i, j int) bool { return out.pipelines[i].Name < out.pipelines[j].Name })
	populated := map[string]bool{"logs": raw.Logs.LogRecordCount() > 0, "traces": raw.Traces.SpanCount() > 0, "metrics": raw.Metrics.MetricCount() > 0}
	active := make([]PipelineDiagnostics, 0, len(out.pipelines))
	selected := mapNode()
	selectedIDs := map[string]bool{}
	for _, pipeline := range out.pipelines {
		if !populated[pipeline.Signal] {
			continue
		}
		active = append(active, pipeline)
		for _, id := range pipeline.Processors {
			if selectedIDs[id] {
				continue
			}
			definition := mappingValue(out.processors, id)
			if hasProviderSubstitution(definition) {
				return nil, fmt.Errorf("processor %s contains an unsupported provider substitution; inline the configuration", id)
			}
			put(selected, id, definition)
			selectedIDs[id] = true
		}
	}
	out.pipelines, out.processors = active, selected
	return out, nil
}

func hasProviderSubstitution(n *yaml.Node) bool {
	if n.Kind == yaml.ScalarNode {
		for i := 0; i < len(n.Value); i++ {
			if n.Value[i] != '{' {
				continue
			}
			dollars := 0
			for j := i - 1; j >= 0 && n.Value[j] == '$'; j-- {
				dollars++
			}
			if dollars%2 == 1 {
				return true
			}
		}
	}
	for _, child := range n.Content {
		if hasProviderSubstitution(child) {
			return true
		}
	}
	return false
}

func validateYAML(n *yaml.Node, depth int) error {
	if depth > 100 {
		return fmt.Errorf("collector YAML exceeds the maximum nesting depth")
	}
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return fmt.Errorf("YAML aliases and anchors are unsupported; provide an explicit configuration")
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Value == "<<" {
				return fmt.Errorf("collector YAML requires string keys without merge keys")
			}
			if seen[key.Value] {
				return fmt.Errorf("collector YAML contains duplicate mapping keys")
			}
			seen[key.Value] = true
		}
	}
	for _, child := range n.Content {
		if err := validateYAML(child, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func componentSequence(node *yaml.Node, optional bool) ([]string, error) {
	if optional && (node == nil || node.Tag == "!!null") {
		return []string{}, nil
	}
	if node == nil || node.Kind != yaml.SequenceNode || (!optional && len(node.Content) == 0) {
		return nil, fmt.Errorf("must be an explicit component sequence")
	}
	ids := make([]string, 0, len(node.Content))
	seen := map[string]bool{}
	for _, item := range node.Content {
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" || !componentID.MatchString(item.Value) {
			return nil, fmt.Errorf("contains an invalid component identifier or provider substitution")
		}
		if seen[item.Value] {
			return nil, fmt.Errorf("contains a duplicate component reference")
		}
		seen[item.Value] = true
		ids = append(ids, item.Value)
	}
	return ids, nil
}

func (t *topology) harness(receiver, capture string) ([]byte, error) {
	for _, endpoint := range []string{receiver, capture} {
		host, _, err := net.SplitHostPort(endpoint)
		if err != nil || host != "127.0.0.1" {
			return nil, fmt.Errorf("harness endpoints must bind IPv4 loopback")
		}
	}
	root := mapNode()
	put(root, "receivers", mapNode("otlp/policy_lab", mapNode("protocols", mapNode("grpc", mapNode("endpoint", strNode(receiver), "max_recv_msg_size_mib", intNode(maxMessageBytes>>20))))))
	put(root, "processors", cloneNode(t.processors))
	put(root, "exporters", mapNode("otlp/policy_lab", mapNode(
		"endpoint", strNode(capture),
		"tls", mapNode("insecure", boolNode(true)),
		"compression", strNode("none"),
		"sending_queue", mapNode("enabled", boolNode(false)),
		"retry_on_failure", mapNode("enabled", boolNode(false)),
		"timeout", strNode("2s"),
	)))
	pipelines := mapNode()
	for _, pipeline := range t.pipelines {
		put(pipelines, pipeline.Name, mapNode("receivers", seqNode([]string{"otlp/policy_lab"}), "processors", seqNode(pipeline.Processors), "exporters", seqNode([]string{"otlp/policy_lab"})))
	}
	put(root, "service", mapNode("pipelines", pipelines, "telemetry", mapNode("logs", mapNode("level", strNode("info")), "metrics", mapNode("level", strNode("none")))))
	return yaml.Marshal(root)
}

func (t *topology) inconclusive(_ time.Duration) []string {
	var reasons []string
	for i := 0; i < len(t.processors.Content); i += 2 {
		id := t.processors.Content[i].Value
		typ := strings.SplitN(id, "/", 2)[0]
		switch typ {
		case "tail_sampling", "groupbytrace":
			reasons = append(reasons, fmt.Sprintf("processor %s completion cannot be established from a settling interval and shutdown", id))
		default:
			continue
		}
	}
	sort.Strings(reasons)
	return reasons
}

func (t *topology) inconclusiveSignals(settle time.Duration) map[string][]string {
	reasons := make(map[string][]string)
	for _, pipeline := range t.pipelines {
		processors := mapNode()
		for _, id := range pipeline.Processors {
			put(processors, id, mappingValue(t.processors, id))
		}
		if scoped := (&topology{processors: processors}).inconclusive(settle); len(scoped) > 0 {
			reasons[pipeline.Signal] = scoped
		}
	}
	return reasons
}

func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func cloneNode(n *yaml.Node) *yaml.Node {
	copied := *n
	copied.HeadComment, copied.LineComment, copied.FootComment = "", "", ""
	copied.Content = nil
	for _, child := range n.Content {
		copied.Content = append(copied.Content, cloneNode(child))
	}
	return &copied
}

func strNode(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }
func boolNode(b bool) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(b)}
}
func intNode(i int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprint(i)}
}
func mapNode(pairs ...any) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i < len(pairs); i += 2 {
		put(n, pairs[i].(string), pairs[i+1].(*yaml.Node))
	}
	return n
}
func put(n *yaml.Node, key string, value *yaml.Node) {
	n.Content = append(n.Content, strNode(key), value)
}
func seqNode(values []string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, value := range values {
		n.Content = append(n.Content, strNode(value))
	}
	return n
}
