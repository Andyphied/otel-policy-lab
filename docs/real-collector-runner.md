# Real Collector processor testing

`otel-policy-lab test --runner otelcol` executes fixtures through real processors in your selected Collector binary. It replaces source receivers and exporters with local OTLP components, captures output in memory and evaluates the existing policies. This tests processor-chain behaviour on the fixture; production receivers, exporters, authentication, queues and backend delivery are outside the test.

## Run

Install or provision your chosen Collector separately. The CLI and reusable GitHub Action never download it. The distribution must include an OTLP gRPC receiver and exporter plus the configured processors.

```sh
otel-policy-lab test \
  --runner otelcol \
  --otelcol-bin ./bin/otelcol-contrib \
  --collector-config ./examples/collector-real.yaml \
  --input ./examples/fixtures/checkout.otlp.json \
  --policy ./examples/policy-pass.yaml \
  --report ./report.json
```

The default remains `--runner fixture`. Selecting `otelcol` requires an explicit `--otelcol-bin`; failures never fall back to simulation. On the real runner, `--validate-collector` is satisfied by starting the generated isolated configuration. The separate `validate` command retains source-config validation.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--collector-start-timeout` | `10s` | Maximum wait for Collector OTLP readiness. |
| `--collector-run-timeout` | `30s` | Overall deadline including startup, submission, settling and shutdown. |
| `--collector-settle-time` | `500ms` | Time after submission before graceful shutdown; zero is allowed. |

Choose a settling interval appropriate for your processors and leave enough overall time for shutdown. For `tail_sampling` and `groupbytrace`, elapsed time alone does not prove completion (upstream processors can buffer their input), so absence-based assertions remain conservative even with a longer interval. The capture server remains active during shutdown so batch buffers can flush. A forced shutdown or failed submission is an execution error, not a policy failure. Cleanup has a small bounded allowance after the run deadline.

## Supported configurations

Use one YAML file with inline processor definitions and at most one pipeline per signal. Only populated fixture signals are submitted, and each requires a source pipeline. Original processor definitions and their pipeline order are retained. Relative processor resources are resolved from the source configuration directory. The supplied binary is resolved before the process changes working directory.

Connectors, cross-pipeline routing, multiple same-signal pipelines, ambiguous YAML and unsupported configuration providers are rejected. Source extensions and production component configuration are omitted. A processor requiring an omitted extension cannot start and produces an error. Arbitrary distributions may lack required components or use incompatible configuration syntax; the supplied binary is the behavioural authority.

Lifecycle guarantees currently target macOS and Linux. Unsupported platforms fail explicitly. Probabilistic and environment-dependent processors may vary between runs. A fixed settling period cannot prove completion of every custom stateful processor; choose a representative fixture and appropriate timing. Known uncertainty is scoped to its signal and yields INCONCLUSIVE for absence-based assertions. Other custom processors remain the caller’s responsibility. Readiness requires both the standard Collector “Everything is ready” lifecycle message and a reachable OTLP gRPC service, preventing transmission to a competing port owner during startup. Distributions omitting that lifecycle message fail startup safely.

## Outcomes

| Outcome | Meaning | Exit |
| --- | --- | --- |
| PASS | Real execution succeeded and every applicable assertion passed. | 0 |
| FAIL | Real execution succeeded and an assertion failed. | 1 |
| INCONCLUSIVE | No proven failure, but a requested assertion lacks reliable evidence. | 4 |
| ERROR | Unsupported harness, missing component, startup, submission, timeout or process failure. | 3 |

Usage/input/report-write errors retain exit 2. `--fail-on-warn` can also return 1. Assertions for signals absent from the input and error preservation without input error spans are inconclusive. If some assertions fail and others are inconclusive, FAIL takes precedence and both remain visible. Uncertain capture cannot establish absence, but observed forbidden data/cardinality violations still fail. Capture of every requested error-span identity establishes preservation even when other decisions are pending. Runner errors take precedence over policy evaluation.

A processor intentionally filtering all input differs from a fixture with no relevant input: the former has observed processing evidence. Forbidden-data checks may pass on fully filtered output; required-resource checks still fail when no records remain. Preservation checks count error span identities (trace ID plus span ID), as in the existing evaluator.

## Isolation and sensitive data

Temporary listeners bind to `127.0.0.1`; the generated exporter points only at the in-process capture server. Production receiver/exporter definitions and source service telemetry are not copied. The harness file is private (`0600`) in a private directory (`0700`), removed on success and failure. Captured telemetry stays in memory and is subject to bounded capture limits.

Practical limits: 4 MiB source YAML, 64 MiB fixture JSON, 16 MiB per submitted signal message, 64 MiB captured protobuf payload, 100,000 captured items, 10,000 capture requests and a 512 MiB regular executable checksum limit. Collector diagnostics retain at most 64 KiB. Graceful Collector shutdown is capped at 3 seconds within the overall deadline; forced cleanup has a 1-second allowance, and sink shutdown has a 500ms fallback. These are resource/error boundaries, not silent truncation thresholds.

The caller-supplied executable and processors are trusted code. **This is not an operating-system network sandbox.** The subprocess inherits the current environment; processors may read files or contact services. Test code and fixtures in an appropriately controlled CI environment. Cleanup terminates descendants in the supplied process group; trusted code that deliberately escapes that group is outside this guarantee. The harness does not promise to suppress arbitrary processor or executable side effects.

Collector diagnostics are bounded in memory and treated as sensitive. Normal output uses safe error categories/component identifiers rather than replaying raw stderr. Reports exclude environment values, raw telemetry, generated configuration and production exporter definitions. Existing matched-value redaction is preserved. Attribute keys, metric names, patterns, component identifiers and user-supplied paths may appear as metadata; avoid secrets in those identifiers.

## Report contract

Real execution emits schema 2; fixture simulation retains schema 1. See [schema 2](report-v2.schema.json). Schema 2 includes:

- `overall_status`, check results and pass/fail/warn/inconclusive/error counts;
- `runner.name: otelcol` and a fixture-local scope description;
- `runner.execution` with binary identity/version/checksum, actual harness checksum, executed pipelines and processor order;
- timeout settings, phase timings, final exit status/code and a safe failure category;
- input/output signal counts, series counts and input-file hashes.

An unavailable version is omitted rather than invented. A process that never started is recorded as `not_started`, without a successful exit code. When execution fails after valid input loading, `--report` still writes an ERROR report.

Normalized record/check ordering is stable. Timestamps, timings, ephemeral endpoints and the generated harness checksum can differ between equivalent runs. Compare normalized checks, counts and processor configuration to assess deterministic behaviour.

Metric cardinality is the union of resource-attribute/label identities for each metric name across all export requests and resources. It does not predict production cardinality or cost. v0.2 corrects the previous per-record undercount for both runners, and avoids collisions from delimiters inside attribute values.

## GitHub Action

The binary must already be present on the runner:

```yaml
- uses: actions/checkout@v4
- uses: ./
  id: policy
  with:
    runner: otelcol
    otelcol-bin: ./bin/otelcol-contrib
    collector-config: ./collector.yaml
    input: ./fixtures/checkout.otlp.json
    policy: ./policy.yaml
    report: ./report.json
    collector-settle-time: 1s
- uses: actions/upload-artifact@v4
  if: always()
  with:
    name: telemetry-policy-report
    path: ${{ steps.policy.outputs.report-path }}
```

The repository integration workflow separately provisions a checksum-pinned test dependency. That developer-test setup is not part of the reusable action or runner.
