# Comparison To Existing Tools

`otel-policy-lab` is intentionally narrow. It is not trying to replace Collector validation, visual config tools, telemetry generators, or production processors.

Most adjacent tooling falls into one of two buckets:

1. **Config-level checks** — lint the Collector YAML for syntax, wiring, or compliance rules.
2. **Output-level checks** — run representative telemetry through a pipeline and assert on what comes out.

`otel-policy-lab` lives in the second bucket. The gap it targets is a standalone, repo-friendly guardrail that combines **separate policy files**, **OTLP fixtures**, **cross-signal governance assertions** (logs, traces, metrics), and **CI-friendly pass/fail reports**. No mature tool in the ecosystem covers that full combination today.

## Comparison Table

| Tool | What it does well | What it does not do | Where `otel-policy-lab` fits |
|------|-------------------|---------------------|-------------------------------|
| `otelcol validate` | Validates Collector config syntax and component wiring against a real Collector binary | Does not evaluate exported telemetry against governance assertions | `otel-policy-lab validate` can call it, then policy-test representative telemetry fixtures |
| [Augur](https://github.com/starkross/augur) | Static analysis of Collector configs using OPA/Rego; catches misconfigurations, security issues, and performance pitfalls from YAML structure | Does not run fixtures through a pipeline or assert on exported telemetry attributes, values, or cardinality | Complements Augur: lint config structure first, then prove representative telemetry still satisfies governance after processing |
| DIY OPA on config | Flexible compliance rules over Collector config JSON in CI (custom Rego policies, org-specific gates) | Requires building and maintaining the harness yourself; evaluates config documents, not pipeline output behavior | `otel-policy-lab` ships the fixture → output → assertion loop out of the box with a purpose-built policy schema |
| [otel-tail-sampling-test](https://github.com/andy-paine/otel-tail-sampling-test) | Behavioral testing for tail sampling: OTLP fixtures, Collector config, expected outcomes, CI integration; embeds the real `tailsamplingprocessor` | Scoped to tail sampling only — does not cover secret redaction, log governance, metric cardinality, or cross-signal policy packs | Same testing *pattern*, broader governance scope across logs, traces, and metrics |
| Collector-contrib `testbed` / `pdatatest` | End-to-end and correctness testing framework for developing Collector components; load generation, golden files, semantic pdata comparison | Internal framework for component authors, not a standalone CLI or policy-as-code product for service teams | `otel-policy-lab` productizes a slice of this idea for pre-deploy governance checks in application repos |
| Custom integration tests (pytest, Go tests, etc.) | Full control: spin up Collector, send synthetic data, assert on backend output | High setup cost, brittle across environments, not reusable as a shared policy artifact | `otel-policy-lab` provides a standard policy file format and report output so teams do not rebuild the harness per service |
| OpenTelemetry Collector processors | Process live telemetry in the runtime path | Are not a CI assertion/reporting layer and can be risky to change without tests | `otel-policy-lab` checks whether representative telemetry satisfies policy before a config ships |
| OTelBin | Visualizes Collector configs and pipeline structure | Does not run policy assertions against telemetry fixtures | `otel-policy-lab` produces pass/fail checks and JSON reports for CI |
| `telemetrygen` | Generates synthetic OTLP telemetry | Does not assert governance outcomes or compare policy expectations | `otel-policy-lab` consumes representative OTLP JSON fixtures and evaluates them |
| Vendor pipeline tooling | Provides backend-specific validation, routing, and cost controls | Often vendor-specific and not always reviewable in source control | `otel-policy-lab` keeps policy files in the repo and makes checks reviewable in PRs |

## Config-Level vs Output-Level

| Question | Config-level tools (`otelcol validate`, Augur, OPA on YAML) | Output-level tools (`otel-policy-lab`, `otel-tail-sampling-test`, integration tests) |
|----------|-------------------------------------------------------------|----------------------------------------------------------------------------------------|
| Is the YAML well-formed? | Yes | Partially (`otel-policy-lab validate` delegates to `otelcol validate`) |
| Are forbidden processors or exporters present? | Yes (Augur, custom OPA) | No — not the primary focus |
| Did this config drop ERROR spans? | No | Yes (with representative fixtures) |
| Did a secret leak into exported attributes? | No | Yes (with representative fixtures) |
| Will metric cardinality stay bounded? | No | Yes (with representative fixtures) |

Use both layers. Config linting catches structural mistakes early. Output-level policy testing catches behavioral regressions that valid YAML cannot reveal.

## Differentiated Value

The useful boundary is policy-as-code for **telemetry governance outcomes**:

- policies are separate from Collector config
- checks run locally and in CI
- output is human-readable and machine-readable
- fixture runner limitations are visible in warnings
- real Collector config validation is available through `otelcol validate`

This makes `otel-policy-lab` a pre-deploy guardrail, not a replacement for production validation or observability backend controls.

## What Is Not a Direct Competitor

These tools solve adjacent problems and are worth using alongside `otel-policy-lab`, but they do not replace its core value:

- **Augur** — excellent for config linting; does not prove output behavior.
- **otel-tail-sampling-test** — the closest behavioral-testing cousin, but single-processor scope.
- **testbed** — powerful for Collector component development; not a drop-in governance harness for service repos.
- **semconvtest** (in development in collector-contrib) — targets semantic convention compliance for component internal telemetry, not user pipeline governance.

## Honest Limitations

The default `fixture` runner does not execute a full Collector pipeline. It simulates only a documented subset and warns when it cannot prove Collector behavior.

Use this tool to catch representative telemetry-policy regressions. Do not treat a passing fixture run as proof that production telemetry will always be safe.
