# Design Decisions

## Why a CI/testing tool instead of a Collector processor

OpenTelemetry Collector processors run in the production telemetry path. A policy-testing processor would:

- add latency and operational risk to live pipelines
- couple governance checks to runtime failures
- be harder to review in pull requests

`otel-policy-lab` runs outside production. Teams can test config changes in CI, local development, and pre-deploy workflows without touching live traffic.

## Why policy is separate from Collector config

Collector configuration describes pipeline mechanics: receivers, processors, exporters, and service graphs.

Policy describes governance outcomes:

- secrets must not be exported
- error traces must be preserved
- metric cardinality must remain bounded
- required resource attributes must be present

Separating these concerns allows:

- platform teams to own baseline policies
- service teams to adopt policies without duplicating Collector internals
- policy changes to be reviewed independently from pipeline changes

## Why MVP uses fixtures

Fixtures provide:

- deterministic tests
- fast CI execution
- readable examples for contributors and users
- no dependency on a running Collector for policy evaluation

Fixtures are not a substitute for production validation. They are a safety net for known scenarios and regression testing.

## Why use Collector pdata for OTLP JSON

The fixture parser uses official OpenTelemetry Collector pdata JSON unmarshalling. That avoids a common failure mode in governance tools: silently accepting a simplified telemetry shape that real OTel components would reject.

The normalized internal model is intentionally smaller than pdata because policy checks need only resource attributes, signal attributes, span status, metric names, and datapoint labels.

## Why forbidden data checks include values and bodies

Sensitive data does not only appear as obvious keys such as `password`. It often leaks through authorization headers, request bodies, log messages, and free-form attribute values.

The MVP supports exact/glob key matching plus regex patterns over resource attributes, signal attributes, log bodies, metric label values, and span names. Span events are not scanned in the MVP. This keeps the policy language small while covering the common leak shapes teams want to catch in CI.

`case_insensitive: true` applies to forbidden keys/labels and to value/body/label-value regex patterns unless a pattern already includes `(?i)`.

## JSON reports redact matched secret material

Forbidden-data failures include match metadata (`kind`, `key`, `pattern`, `value_length`, `index`, `metric`) but never echo raw attribute values or log bodies. Required-resource failures include only record indexes and missing key names, not full `resource_attributes` maps. This keeps CI artifacts shareable without leaking fixture secrets.

## Required resource attributes mean every record

Required resource attributes are evaluated per exported item. A signal passes only when every log, span, or metric record in the evaluated output carries all required resource attributes.

This is stricter than checking that at least one record has the key, and it matches the report wording (`all logs include service.name`).

## Why include real Collector config validation

The `validate` command runs `otelcol validate --config <path>` so users can catch real Collector configuration errors without pretending the MVP executes a full pipeline.

This is deliberately narrower than `RealCollectorRunner`: config validation proves syntax/component validity, while policy evaluation still operates on fixture output.

## Why Go

Go was chosen because:

- OpenTelemetry Collector is written in Go
- single-binary CLI distribution is simple
- testing and module ecosystem are mature
- standard OTLP integration and Collector subprocess management fit the ecosystem

## FixtureRunner tradeoff (MVP)

The MVP `FixtureRunner` intentionally simulates only a subset of Collector behavior:

- `attributes/*` processors with `delete` actions on resource attributes, signal attributes, and metric datapoint labels
- `filter/*` processors that drop debug logs via regexp match hints
- no-content-effect processors such as `batch` as explicit no-ops

It does **not**:

- execute real Collector components
- validate full pipeline graphs
- emulate sampling nondeterminism
- implement full OTTL or transform processor semantics
- honor attributes processor include/exclude matching

The runner emits warnings for unsupported processors and partial simulations. A policy pass with runner warnings should be treated as a useful regression signal, not proof of real Collector behavior.

Trace preservation checks are fixture-local with the default runner because it does not simulate span dropping or sampling. When the fixture runner emits its trace-preservation warning, a passing preservation check is annotated in the report so users do not confuse fixture equality with real sampling behavior. The real runner exercises actual sampling and preserves this distinction in its provenance.

## Go and pdata version pinning

`go.mod` pins Go `1.22.4` and `go.opentelemetry.io/collector/pdata v1.25.0` so CI, local builds, and OTLP JSON parsing stay aligned with a known Collector pdata release line. Upgrade both together when bumping pdata.

## RealCollectorRunner design (v0.2)

Use a caller-supplied executable and standard OTLP gRPC boundaries rather than embedding processors or requiring a contrib file exporter. Preserve inline processor YAML as nodes and validate topology independently from the simulator. The binary remains responsible for component configuration and processor behaviour.

Replace receivers/exporters with loopback harness components; reject connectors and multiple pipelines per signal. Keep full pdata until transport is complete and use the existing normalized model for policy evaluation. Run from the source configuration directory so relative processor resources have a documented base.

Capture remains alive during graceful shutdown. Timeouts, cancellation and abnormal exit never become policy failures or trigger fixture fallback. The trusted subprocess inherits environment variables; source exporter isolation is not an OS network sandbox. Raw diagnostics remain private and bounded.

Real reports use schema 2 with explicit inconclusive/error outcomes and execution provenance. Fixture schema 1 and golden terminal formatting remain stable. v0.2 also corrects cardinality aggregation across records and ambiguous series identifiers for both runners.

## Exit code semantics

| Code | Meaning |
| ------ | --------- |
| 0 | All checks passed (warnings allowed unless `--fail-on-warn`) |
| 1 | Policy failure |
| 2 | Usage, parsing, or report write error |
| 3 | Pipeline runner execution error |
| 4 | Real-runner assertion evidence is inconclusive |

## Deliberate MVP limitations

- `preserve` supports only `status.code == "ERROR"`
- OTLP JSON fixtures only (no protobuf in MVP)
- metric series counting uses unique resource + datapoint label sets inside the fixture, not production cardinality over time
- span event names and attributes are not scanned yet
- no plugin framework yet

These limits keep the first version shippable while leaving clear extension seams.
