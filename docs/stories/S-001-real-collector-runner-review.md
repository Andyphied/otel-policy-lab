# S-001: Real Collector runner — pre-implementation review

Date: 2026-09-29

Status: Approved for implementation on 2026-09-29

Target: v0.2.0

This review maps the supplied S-001 draft to the repository before implementation. The user approved the three scope decisions and clarified isolation boundary on 2026-09-29. The remaining sections retain the implementation proposal; test evidence is recorded separately in the test plan.

## Decisions for approval

Retain the three proposed scope decisions:

1. Execute real processor chains with replacement loopback OTLP receivers and exporters. Report that production receivers, exporters and delivery behaviour were not exercised.
2. Require an explicitly supplied Collector binary for the real runner. Do not download binaries or fall back to the fixture runner. Keep existing fixture and validation defaults compatible.
3. Reject connectors, cross-pipeline routing and multiple pipelines for the same signal. Reject ambiguous provider-based configuration and unsupported dependencies before sending telemetry.

One security clarification needs approval with these decisions: replacing receivers and exporters prevents the harness from using production exporter configuration, but it does not provide an operating-system network sandbox for an arbitrary executable or processor. The draft both permits environment-dependent processors and prohibits all connections to source-configured endpoints. Those promises cannot both be guaranteed by configuration rewriting alone. Proposed v0.2 scope is trusted caller-supplied code, no production receiver/exporter configuration in the harness, and explicit documentation of inherited environment and possible processor side effects. If a blanket outbound-network prohibition is required, add enforceable network isolation and narrow platform support before implementation.

## Findings in the current code

| Area | Current behaviour | Required change |
| --- | --- | --- |
| `internal/runner/runner.go` | `PipelineRunner` already separates execution from evaluation; only `fixture` is selectable. | Keep the interface and add explicitly configured real execution and structured diagnostics. |
| `internal/telemetry/otlp.go` | Parses pdata and immediately normalizes it. Normalization discards timestamps, scopes, severity, metric values/types and other processor inputs. | Retain full pdata for transmission. Normalize input and captured output only for existing policy evaluation. Never reconstruct OTLP from `telemetry.Set`. |
| `internal/runner/runner.go` | Fixture configuration parsing merges same-signal pipeline processor lists. | Give the real runner its own strict topology parser using YAML nodes. Do not reuse the simulator's permissive topology handling. |
| `internal/cli/cli.go` | Execution uses `context.Background()`; validation occurs before runner selection; binary defaults to `otelcol`. | Add signal cancellation and real-runner timeout options. Require an explicit binary for `otelcol`. Validate the generated harness for real execution; retain source validation semantics for the existing fixture path. |
| `internal/collector/validate.go` | Single-process command context and an unbounded output buffer. | Use a separate lifecycle manager with bounded private diagnostics and process-tree cleanup. Do not reuse this implementation unchanged. |
| `internal/report/report.go` | Schema 1 supports PASS/FAIL/WARN; runner errors bypass JSON reporting. | Add versioned real-runner reporting, ERROR and INCONCLUSIVE, safe provenance, phase timings and exit information. Preserve established fixture output and exit codes. |
| `internal/evaluator/evaluator.go` | Cardinality checks each normalized metric record separately; empty error-span input passes with a skipped-check message. | Define metric grouping across capture requests and resources before evaluation. Treat assertions without qualifying input as inconclusive for real runs through a general applicability mechanism, without branching on runner name in the evaluator. |
| `action.yml` | No runner input; the binary is forwarded only when validation is enabled. | Forward runner, explicit binary and timeout inputs. Use environment variables and quoted shell arguments for action inputs. Expose the report path even on non-passing runs. |

## Proposed execution contract

1. Read the fixture once into full pdata plus its normalized policy view. Read the single source configuration into YAML nodes. Reject duplicate/ambiguous keys, unresolved structural includes, undefined components, missing pipelines for populated fixture signals and unsupported topology. Preserve referenced processor definitions and sequence order without processor-specific decoding.
2. Resolve the supplied executable before changing the child working directory. Resolve processor-relative resources from the source configuration directory and document that convention. Create a `0700` temporary directory and a `0600` harness file; delete both on every exit path.
3. Start a loopback-only OTLP gRPC capture server. Build a new configuration from an allowlist of harness components and referenced processors. Omit source receivers, exporters, connectors, extensions and service telemetry configuration. Dependencies on omitted components must fail clearly.
4. Configure a loopback OTLP receiver and local capture exporter. Disable exporter queues/retries where supported by the chosen harness compatibility floor so transport errors are observable and ambiguous retries cannot duplicate the fixture. Bound capture memory and message sizes; overflow is an ERROR, never silent truncation.
5. Start the Collector in its own process group on supported Unix platforms. Retain bounded diagnostics in memory. Obtain bounded version information without persisting arbitrary process output. Treat version lookup as part of the overall deadline.
6. Wait for OTLP readiness while monitoring child exit and the startup deadline. Use bounded pre-submission retries for port allocation collisions. Do not blindly retry fixture submission after an ambiguous transport failure. Treat OTLP partial-success responses as submission failures.
7. Send only populated signals and start the settling interval after all submissions finish. Keep the capture server running throughout graceful Collector shutdown. An internal shutdown deadline must fit within the overall run deadline; forced termination produces ERROR.
8. Reap the process and its descendants, drain capture handlers, close connections and listeners, and remove temporary files before returning. Cancellation must follow the same cleanup path. Initially support macOS/Linux lifecycle guarantees and reject unsupported platforms explicitly until equivalent cleanup is tested.
9. Normalize captured telemetry in stable order, with a defined cross-request metric grouping rule. Evaluate with the existing policy engine. Classify assertion applicability independently of the runner implementation.

The overall timeout covers version lookup, startup, submission, settling and graceful shutdown. Cleanup after expiration has its own small bounded allowance to terminate and reap the process. A startup probe alone does not prove fixture acceptance. Successful OTLP submission alone does not prove every stateful processor reached a decision; document settling limits and return inconclusive when reliable evaluation is known to be unavailable.

## Result and compatibility proposal

| Outcome | Meaning | Proposed CLI exit |
| --- | --- | --- |
| PASS | Successful real run; every requested assertion is applicable and passes. | 0 |
| FAIL | Successful real run; at least one evaluated policy assertion fails. | 1 |
| INCONCLUSIVE | Successful run with no proven policy failure, but at least one requested assertion cannot be evaluated reliably. | 4 |
| ERROR | Harness, compatibility, timeout, transport or process failure. | 3 |

Usage and input parsing errors retain exit 2. Fixture runner behaviour remains compatible, including WARN and `--fail-on-warn`. Real-runner warnings are separate from the four overall outcomes. Reports retain per-check inconclusive information even when a proven failure determines overall FAIL; execution ERROR takes precedence over incomplete policy evaluation.

Use schema 2 for real-runner reports and retain the schema 1 fixture contract. Include binary identity/version, binary checksum when available, actual generated-config checksum, executed pipeline names and ordered processors, input/output counts, configured deadlines, phase timings, and process termination information. A failure before process start records “not started”, not exit code 0. Write a safe ERROR report when `--report` is supplied and enough inputs are available.

Raw diagnostics, environment values, complete configuration and raw captured telemetry are excluded from reports and normal terminal output. Missing-component errors should expose only validated component identifiers and a bounded category, not arbitrary Collector log lines. Existing secret-redaction regression tests remain required.

Deterministic comparison excludes timestamps, durations, temporary paths and the actual harness checksum when ephemeral ports differ. Pipeline ordering, normalized check ordering and policy outcomes must remain stable for deterministic configurations.

## Implementation sequence after approval

1. Add full-fidelity fixture parsing and strict topology/harness generation with unit coverage. Keep simulator paths unchanged.
2. Add capture, sending and process lifecycle modules. Exercise cleanup with a controllable helper subprocess, including descendants and timeout paths.
3. Add real-runner orchestration, generic applicability/grouping support where necessary, safe schema 2 reports and CLI selection. Add action inputs and outputs.
4. Add an integration suite against an exact pinned Collector-contrib release and verified binary checksum. CI must explicitly provision its test dependency; neither the CLI nor the reusable action installs a Collector. Keep the integration command usable with an already-provisioned binary.
5. Update the README, confidence model, architecture, design decisions, comparison, failure modes, roadmap, report schema documentation and release notes. Record acceptance evidence before declaring v0.2 complete.

## Acceptance evidence to require

| Criteria | Required evidence |
| --- | --- |
| AC1–2 | Real attributes deletion and an intentional deletion regression, with forbidden values absent from reports. |
| AC3 | Two real filter expressions distinguish a debug log from a legitimate message containing “debug”. |
| AC4 | Deterministic sampling drops known error-span identities; retained/total counts are asserted. |
| AC5 | Reversed transform/filter or attribute/filter order produces different captured output. Include conditional attribute matching. |
| AC6 | Batch holds the fixture until graceful shutdown; capture remains available for the final flush. |
| AC7 | Source exporter points at a monitored local trap endpoint; no connection occurs. Also inspect generated YAML for source exporter configuration and non-loopback harness endpoints. |
| AC8 | Connectors, duplicate same-signal pipelines, provider indirection and undefined processor references fail before fixture transmission and cannot yield PASS. |
| AC9 | Real missing OTLP/processor component and invalid processor configuration failures yield safe compatibility errors. Use an additional minimal distribution if needed to demonstrate missing OTLP support. |
| AC10 | Helper-process tests cover startup failure, abnormal exit, cancellation, total/start/shutdown deadlines and child processes; assert process reaping, reusable ports and removed private directories. |
| AC11 | Success/error JSON provenance, real-runner terminal output and no raw diagnostics/fixture secrets. |
| AC12 | Existing fixture unit, CLI and report golden tests remain unchanged and pass. |
| AC13 | Repeated real deterministic runs produce identical normalized policy outcomes across multiple capture requests and varying request arrival order. |
| AC14 | Action smoke run uses a preinstalled pinned binary, selects `otelcol`, returns the proper status and exposes the report on success and failure. |

Include three-signal passthrough, transform behaviour, capture limits, partial-success responses and source-resource path resolution. Measure the stated 15-second target on the pinned GitHub-hosted Linux job rather than extrapolating from local execution.

Baseline verification: `go test ./...` passed on 2026-09-29 with the local toolchain, cached dependencies, module writes disabled and a temporary build cache. No implementation or real-Collector integration verification has been performed as part of this pre-approval review.
