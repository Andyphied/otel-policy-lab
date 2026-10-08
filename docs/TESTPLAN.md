# S-001 test plan and acceptance evidence

Verified locally on 2026-09-29 on macOS arm64. The user approved the processor-only scope, explicit binary, restricted topology and trusted-code boundary before implementation.

## Reproduce

Standard unit/regression suite (real integrations skip without an explicit binary):

```sh
make test
make vet
make build
```

Mandatory real integration and race suite with an already provisioned Collector:

```sh
OTELCOL_BIN=/absolute/path/to/otelcol-contrib \
OTELCOL_INTEGRATION_REQUIRED=1 \
go test -race -count=1 ./...
```

For development/CI only, `bash scripts/provision-test-collector.sh /your/test-directory` explicitly provisions the pinned dependency and verifies its archive checksum. The product CLI and reusable action never invoke this script. `OTELCOL_INTEGRATION_REQUIRED=1` makes the integration suite fail when its binary is missing.

The pinned archive is official Collector-contrib v0.120.0. The downloaded darwin arm64 artifact's own `--version` reports `0.120.1`; reports intentionally retain that observed value rather than rewriting it to the archive tag. Archive SHA256 is pinned in the provisioning script; execution also records the executable SHA256.

## Local results

- `go test ./...` passed, including pinned real execution.
- Final `go test -race -count=1 ./...` passed with `OTELCOL_BIN` and `OTELCOL_INTEGRATION_REQUIRED=1`. Runner package: 30.851s; CLI package: 18.566s. These are suite durations, not per-fixture runtime.
- `go vet ./...` and a CLI build passed.
- Existing fixture CLI/report golden tests passed unchanged.
- The real checkout example passed all eight checks in approximately 2.80s locally.
- `git diff --check`, provisioning-script shell syntax and workflow YAML parsing passed.
- Final read-only architecture review found the earlier uncertainty, provider, deadline/read and readiness issues addressed, with no remaining critical blocker.

## Acceptance mapping

| AC | Automated evidence |
| --- | --- |
| 1–2: real deletion/regression | `TestOTelcolIntegrationDeletionRegression`; `TestRealCLIOutcomesAndReportSecrecy` exercises actual PASS/FAIL and report redaction. |
| 3: actual filter semantics | `TestOTelcolIntegrationActualOTTLFilter` differentiates debug-level records from legitimate bodies containing “debug”. |
| 4: sampling retention | `TestOTelcolIntegrationSamplingErrorRetention` compares deterministic 0%/100% sampler output and retained/total error-span identities. |
| 5: ordering | `TestOTelcolIntegrationProcessorOrderAndTransform`, `TestOTelcolIntegrationConditionalAttributes`. |
| 6: batch flush | `TestOTelcolIntegrationBatchShutdownFlush` uses a long batch timeout so final output requires shutdown flushing. |
| 7: source exporter isolation | `TestOTelcolIntegrationProductionExporterNeverContacted` monitors a trap endpoint; `TestHarnessPreservesOrderedDefinitionsAndExcludesProduction` inspects generated YAML. |
| 8: unsupported topology | `TestTopologyRejectsAmbiguousOrUnsupportedInputs`, `TestTopologyRequiresPipelineForPopulatedSignal`, `TestHarnessSelectsOnlyPopulatedSignals`. |
| 9: component compatibility | `TestOTelcolIntegrationSafeCompatibilityErrors` checks actual absent processor and invalid configuration failures; safe hint tests cover validated identifiers and suppression of arbitrary diagnostic text. |
| 10: process/listener/temp cleanup | `TestCollectorRunDeadlinesCleanPrivateHarness` covers startup timeout, overall timeout, cancellation and early abnormal exit; verifies reaping, both ports reusable, config-directory cwd, private permissions and directory removal. Additional `TestCollectorProcess*` tests cover descendants and graceful/forced shutdown. |
| 11: provenance | Real-runner integration, CLI outcome tests and `TestRealOutcomesAndSchema` verify runner, version, hashes, schema, status and safe error reports. |
| 12: fixture compatibility | Existing fixture runner, CLI and report goldens pass. v0.2's generic cross-record cardinality correction is explicitly documented in release notes. |
| 13: determinism | `TestOTelcolIntegrationThreeSignalsAndRepeatedOutput`, `TestCanonicalizeCaptureFragments`, and deterministic forbidden-match/report tests. |
| 14: action | `.github/workflows/collector-integration.yml` provisions the pinned dependency, invokes the actual composite action for PASS/FAIL, asserts report outputs/schema/secrecy and uploads reports on failure. Hosted execution remains pending CI. |

## Additional failure/security coverage

- `TestOTLPRetainsProcessorInputs`: typed attributes, log severity, timestamps, scope, span events/parent identity and metric type/value survive parsing/normalization.
- Capture tests: bounded memory/items/requests, transport failures, typed multi-request capture, rejected partial success without retries, and stable failure/readiness markers across log truncation/write boundaries.
- Evidence tests: absent fixture signals, no qualifying error spans, scoped asynchronous uncertainty, conclusive captured violations retained as FAIL, and complete observed error-span preservation.
- Cardinality tests: union across resources and requests, duplicate point deduplication, and collision-resistant series identities.
- Metadata tests: nonregular files do not trigger unbounded reads and canceled metadata generation stops.
- Version tests: bounded extraction without raw diagnostic leakage and timeout cleanup during version lookup.

The real filter, sampling, conditional attribute and ordering regressions demonstrate behaviours the fixture simulator cannot establish.

## Remaining release checks and supported limits

The GitHub-hosted Linux action smoke and three-signal <=15s timing assertion are configured but have not run in this local session. A custom distribution omitting the OTLP receiver/exporter was not available locally; actual missing-processor integration and generic compatibility categorization are verified. Before tagging a release, run CI and optionally supply such a minimal distribution to confirm a safe ERROR report with `missing_component` and no transmitted fixture.

Relative working directory is verified through the helper process. An actual environment-specific processor with a relative resource file should be exercised by its owner using that distribution and file; the harness retains the definition and launches from the source configuration directory.

Lifecycle guarantees apply to macOS/Linux process groups. Trusted code that deliberately escapes its group or makes other network calls is outside configuration isolation. `tail_sampling` and `groupbytrace` do not receive absence-based PASS merely because a configured wait elapsed; upstream buffering makes that inference unsafe. Arbitrary custom processors may have additional completion semantics the harness cannot discover. See [usage and bounds](real-collector-runner.md).
