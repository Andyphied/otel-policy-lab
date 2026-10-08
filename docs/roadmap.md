# Roadmap

## Near term

- [x] GitHub Action for running `otel-policy-lab test` in CI
- [x] Collector config validation through `otelcol validate`
- [ ] SARIF output for policy failures
- [x] `RealCollectorRunner` using a caller-supplied `otelcol` subprocess and local OTLP capture
- [ ] OTLP protobuf fixture support
- [x] Collector version, binary/config hashes and execution metadata in real-runner JSON reports
- [x] Golden terminal and JSON report fixtures for release stability
- [ ] Richer preserve predicates beyond `status.code == "ERROR"`
- [ ] Attributes include/exclude simulation or explicit per-check unsupported status

## Medium term

- [ ] Cost estimation plugins (series growth, ingest volume projections)
- [ ] Secret scanning integrations (TruffleHog, Gitleaks-style checks on exported attributes)
- [ ] Additional preserve expressions beyond `status.code == "ERROR"`
- [ ] Probabilistic sampling policy checks
- [ ] Multi-fixture test suites and policy profiles per environment

## Long term

- [ ] Policy registry and shared baseline packs for platform teams
- [ ] Diff mode comparing output between two Collector configs
- [ ] Live OTLP replay from recorded exports
- [ ] Dashboard for trend analysis of policy failures across services

## Contribution priorities

If you are interested in contributing, these areas have the highest impact:

1. `RealCollectorRunner` with deterministic telemetry capture
2. SARIF reporting
3. Richer fixture tooling (sanitized production export -> fixture generator)
4. Documentation and examples for common governance policies

## Real-runner follow-ups

- Container execution and Windows process lifecycle support
- Multiple same-signal pipelines, connectors and cross-pipeline routing
- Configuration providers and multi-file merge support
- Repeated probabilistic sampling assertions and configuration diff mode
- Full receiver/exporter integration tests outside the processor harness
