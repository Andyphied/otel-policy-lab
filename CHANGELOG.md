# Changelog

All notable changes to this project are documented in this file.

## Unreleased

## [0.1.2] - 2026-06-30

Release notes: [`docs/releases/v0.1.2.md`](docs/releases/v0.1.2.md)

### Added

- GitHub Action Marketplace branding (`shield` icon, `blue` badge) in `action.yml`.

## [0.1.1] - 2026-06-30

Release notes: [`docs/releases/v0.1.1.md`](docs/releases/v0.1.1.md)

### Changed

- GitHub Action uses `go-version-file` from `go.mod` instead of a `go-version` input.

### Added

- `cmd/otel-policy-lab/main.go` CLI entrypoint with link-time version injection.
- `.golangci.yml` and golangci-lint CI job.
- `action-smoke` CI job that mirrors composite-action build and runs the passing example.
- Secret-redaction fixture and evaluator tests for traces/metrics required-resource failures.
- Traces-only and metrics-only OTLP parse tests.

### Documentation

- Expanded comparison with existing tools (config-level vs output-level checks).
- Documented vacuous passes on empty signals and fixture runner label deletion behavior.
- README opening reframed around failure modes.

## [0.1.0] - 2026-06-19

Release notes: [`docs/releases/v0.1.0.md`](docs/releases/v0.1.0.md)

### Security

- JSON reports no longer echo raw forbidden attribute values, log bodies, or full resource attribute maps on required-resource failures; failures include `value_length` or missing-key metadata instead.

### Added

- Resource attribute scanning for forbidden keys/values across logs, traces, and metrics.
- Resource attribute deletion in the fixture runner's attributes processor simulation.
- `forbidden_label_value_patterns` for metric label value scanning.
- Span name scanning via `traces.forbidden_value_patterns`.
- Deterministic ordering for forbidden-match details in JSON reports.
- GitHub Action provisions Go, pins toolchain, and builds with `CGO_ENABLED=0`.
- Golden terminal and JSON report fixtures for release stability.

### Changed

- `case_insensitive: true` now also applies to value/body/label-value regex patterns unless `(?i)` is already present.
- Error-trace preservation passes are annotated when the fixture runner cannot simulate span dropping.
- GitHub Action description matches fixture-based policy evaluation positioning.

### Documentation

- Documented Go/pdata version pinning, span event scanning scope, and report redaction behavior.

[0.1.2]: https://github.com/Andyphied/otel-policy-lab/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/Andyphied/otel-policy-lab/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/Andyphied/otel-policy-lab/releases/tag/v0.1.0
