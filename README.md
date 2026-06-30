# otel-policy-lab

**Catch dangerous OpenTelemetry Collector config changes in CI — before they drop error traces, leak secrets, or blow up your metrics bill.**

A misconfigured Collector processor can silently export an `authorization` header, delete the error spans you need during an incident, or multiply metric cardinality overnight. `otel-policy-lab` treats these as testable regressions: you give it a representative OTLP fixture, your Collector config, and a separate policy file, and it produces a CI-friendly pass/fail report.

It does **not** replace the Collector or run your production pipeline. It is a fast, honest guardrail with an explicit confidence model — it tells you what it simulated and what it did not.

> Treat observability pipeline changes like code: testable, reviewable, and safe before production.

## Why this exists

OpenTelemetry Collector configurations are powerful and risky. A misconfigured processor can:

- drop error traces during an incident
- export secrets in log attributes or resource attributes
- explode metric cardinality and observability cost

Collector already handles receiving, processing, transforming, filtering, sampling, batching, and exporting telemetry. This tool does **not** replace Collector. It sits outside the runtime path and validates representative telemetry using fixtures, explicit policy assertions, and a deliberately narrow simulated runner.

### Runtime path

```text
App -> OpenTelemetry Collector -> Observability backend
```

### Testing path

```text
Telemetry fixture -> Collector config -> Captured output -> Policy assertions
```

## Quickstart

### Prerequisites

- Go 1.22.4 (matches `go.mod`; use `GOTOOLCHAIN=local` if your installed toolchain is newer)

### Build

```sh
make build
```

### Run the example

```sh
./otel-policy-lab test \
  --collector-config ./examples/collector.yaml \
  --input ./examples/fixtures/checkout.otlp.json \
  --policy ./examples/policy.yaml \
  --report ./report.json
```

### Example output

```text
PASS no forbidden log resource attributes, attributes, values, or bodies exported
PASS all logs include service.name, deployment.environment
PASS 100% of error traces preserved (fixture-local; runner does not drop spans)
PASS no forbidden span resource attributes, attributes, values, or names exported
PASS all traces include service.name
PASS no forbidden metric resource attributes, labels, or label values exported
FAIL metric http.server.duration has 28,441 unique series; limit is 10,000
PASS all metrics include service.name
WARN log volume reduced by 71%; verify debug logs are not over-filtered
WARN fixture runner partially simulated processor filter/drop_debug_logs; full filter/OTTL semantics are not supported
WARN fixture runner does not simulate span dropping or sampling; trace preservation checks are fixture-local

Runner: fixture (simulated processors: 4, unsupported processors: 0)
Result: FAIL (7 pass, 1 fail, 3 warn)
```

The command exits non-zero on policy failure, making it suitable for CI gates.

## Policy file

Policies are intentionally separate from Collector configuration. Collector config describes **how** telemetry is processed. Policy describes **what must remain true** after processing. This lets platform teams own reusable governance rules independently from service-specific Collector pipelines.

See [`examples/policy.yaml`](examples/policy.yaml).

Supported MVP assertions:

- **Logs**: forbidden resource/attribute keys, forbidden attribute values, forbidden log body patterns, required resource attributes
- **Traces**: preserve error traces when evaluated output differs, forbidden resource/attribute keys, forbidden attribute values (including span names), required resource attributes
- **Metrics**: forbidden resource/label keys, forbidden label value patterns, unique max series per metric, required resource attributes

## Fixture format

Fixtures use OTLP JSON with `resourceLogs`, `resourceSpans`, and `resourceMetrics`. The parser uses official OpenTelemetry Collector `pdata` JSON unmarshalling rather than a local approximation.

See [`examples/fixtures/checkout.otlp.json`](examples/fixtures/checkout.otlp.json).

## Collector config validation

You can validate config syntax against a real Collector binary:

```sh
otel-policy-lab validate \
  --collector-config ./examples/collector.yaml \
  --otelcol-bin otelcol
```

You can also gate a policy test with real Collector validation:

```sh
otel-policy-lab test \
  --collector-config ./examples/collector.yaml \
  --input ./examples/fixtures/checkout.otlp.json \
  --policy ./examples/policy.yaml \
  --validate-collector
```

This validates Collector configuration, but it still does not execute the full telemetry pipeline.

## JSON report

When `--report` is provided, the tool writes a machine-readable report with:

- overall status and pass/fail/warn counts
- individual check results
- input, policy, and collector config metadata
- `schema_version` for downstream consumers
- runner coverage metadata
- summary statistics

Failure details redact raw secret values. Forbidden-data matches include metadata only (`value_length`, keys, patterns). Required-resource failures include missing key names and record indexes, not full attribute maps.

## GitHub Action

Use the repository action in CI. The action provisions Go, builds from the checked-out action source, and runs policy checks:

```yaml
steps:
  - uses: actions/checkout@v4
  - uses: andyphied/otel-policy-lab@v0.1.1
    with:
      collector-config: ./collector.yaml
      input: ./fixtures/checkout.otlp.json
      policy: ./policy.yaml
      report: ./otel-policy-report.json
```

## MVP runner note

The default `fixture` runner is deterministic and simulates a small subset of Collector processor behavior: attribute deletion on resource attributes, signal attributes, and metric labels, plus narrow debug log filtering. It does **not** execute a real Collector binary.

The runner emits warnings when processors are unsupported or only partially simulated. Those warnings are part of the tool's confidence model: a pass with unsupported processors should not be treated as proof of production behavior.

This is an intentional MVP tradeoff. The `PipelineRunner` interface is designed so a future `otelcol` runner can shell out to a real Collector and capture exported telemetry.

See [`docs/design-decisions.md`](docs/design-decisions.md).

## What this does not do

- Replace OpenTelemetry Collector
- Run in the production telemetry path
- Guarantee production safety from fixtures alone
- Support every Collector processor in MVP
- Implement full OTTL/filter/transform/sampling semantics
- Provide cost estimation or secret scanning (planned)

## Documentation

- [Architecture](docs/architecture.md)
- [Design decisions](docs/design-decisions.md)
- [Failure modes](docs/failure-modes.md)
- [Comparison to existing tools](docs/comparison-to-existing-tools.md)
- [Roadmap](docs/roadmap.md)

## Development

```sh
make test
make vet
make example
```

## License

Apache-2.0
