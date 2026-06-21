# Changelog

All notable changes to this project are documented in this file.

## Unreleased

### Security

- JSON reports no longer echo raw forbidden attribute values, log bodies, or full resource attribute maps on required-resource failures; failures include `value_length` or missing-key metadata instead.

### Added

- Resource attribute scanning for forbidden keys/values across logs, traces, and metrics.
- Resource attribute deletion in the fixture runner's attributes processor simulation.

- `forbidden_label_value_patterns` for metric label value scanning.
- Span name scanning via `traces.forbidden_value_patterns`.
- Deterministic ordering for forbidden-match details in JSON reports.
- GitHub Action provisions Go, pins toolchain, and builds with `CGO_ENABLED=0`.

### Changed

- `case_insensitive: true` now also applies to value/body/label-value regex patterns unless `(?i)` is already present.
- Error-trace preservation passes are annotated when the fixture runner cannot simulate span dropping.
- GitHub Action description now matches fixture-based policy evaluation positioning.

### Documentation

- Documented Go/pdata version pinning, span event scanning scope, report redaction, vacuous passes on empty signals, and fixture runner label deletion behavior.
- README opening reframed around failure modes; action example pins `@v0.1.0`.

### Tooling

- Added `.golangci.yml`, CI action-smoke job, and `go-version-file: go.mod` in CI and composite action.
- Added traces-only/metrics-only OTLP parse tests and traces/metrics required-resource redaction tests.
