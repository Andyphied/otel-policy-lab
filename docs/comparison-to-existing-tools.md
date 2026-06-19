# Comparison To Existing Tools

`otel-policy-lab` is intentionally narrow. It is not trying to replace Collector validation, visual config tools, telemetry generators, or production processors.

| Tool | What it does well | What it does not do | Where `otel-policy-lab` fits |
|------|-------------------|---------------------|-------------------------------|
| `otelcol validate` | Validates Collector config syntax and component wiring against a real Collector binary | Does not evaluate exported telemetry against governance assertions | `otel-policy-lab validate` can call it, then policy-test representative telemetry fixtures |
| OpenTelemetry Collector processors | Process live telemetry in the runtime path | Are not a CI assertion/reporting layer and can be risky to change without tests | `otel-policy-lab` checks whether representative telemetry satisfies policy before a config ships |
| OTelBin | Visualizes Collector configs and pipeline structure | Does not run policy assertions against telemetry fixtures | `otel-policy-lab` produces pass/fail checks and JSON reports for CI |
| `telemetrygen` | Generates synthetic OTLP telemetry | Does not assert governance outcomes or compare policy expectations | `otel-policy-lab` consumes representative OTLP JSON fixtures and evaluates them |
| Vendor pipeline tooling | Provides backend-specific validation, routing, and cost controls | Often vendor-specific and not always reviewable in source control | `otel-policy-lab` keeps policy files in the repo and makes checks reviewable in PRs |

## Differentiated Value

The useful boundary is policy-as-code for telemetry governance:

- policies are separate from Collector config
- checks run locally and in CI
- output is human-readable and machine-readable
- fixture runner limitations are visible in warnings
- real Collector config validation is available through `otelcol validate`

This makes `otel-policy-lab` a pre-deploy guardrail, not a replacement for production validation or observability backend controls.

## Honest Limitations

The default `fixture` runner does not execute a full Collector pipeline. It simulates only a documented subset and warns when it cannot prove Collector behavior.

Use this tool to catch representative telemetry-policy regressions. Do not treat a passing fixture run as proof that production telemetry will always be safe.
