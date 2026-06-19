# Roadmap

## Near term

- [x] GitHub Action for running `otel-policy-lab test` in CI
- [x] Collector config validation through `otelcol validate`
- [ ] SARIF output for policy failures
- [ ] `RealCollectorRunner` using `otelcol` subprocess
- [ ] OTLP protobuf fixture support
- [ ] Collector version metadata in JSON reports
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
