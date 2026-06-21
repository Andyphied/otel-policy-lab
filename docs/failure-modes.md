# Failure Modes

`otel-policy-lab` is useful, but it is not a guarantee of production safety. Teams should understand these limitations.

## False positives

A check may fail even when production would be fine.

Examples:

- fixture metric cardinality exceeds policy limit, but production traffic uses bounded routes
- required resource attributes are missing in a synthetic fixture but always present in real SDK instrumentation
- debug log filtering warning triggers on a fixture designed to include debug logs

Mitigation:

- maintain realistic fixtures derived from sanitized production samples
- tune policy thresholds per environment
- use warnings for informational checks, failures for hard governance rules

## False negatives

A check may pass even when production would fail.

Examples:

- fixture does not include secret-bearing attributes that appear in real traffic
- fixture omits rare error traces that sampling would drop in production
- `FixtureRunner` does not execute the real Collector config
- forbidden patterns do not cover a sensitive value format used in production

Mitigation:

- rotate fixtures from production-like scenarios
- review and expand forbidden value/body patterns as services evolve
- add future `RealCollectorRunner` validation before high-risk deploys
- combine policy checks with secret scanning and cost monitoring in production

## Nondeterministic sampling

Collector tail sampling and probabilistic head sampling can produce different outputs across runs.

MVP does not model sampling nondeterminism. Preservation checks are deterministic against fixtures.

Mitigation:

- test unsampled or fully sampled scenarios explicitly
- document sampling policies separately
- future support for probabilistic bounds instead of exact preservation

## Fixture drift

Fixtures can become stale as application behavior evolves.

Symptoms:

- policies pass in CI while production telemetry shape has changed
- missing new attributes or span names in fixtures

Mitigation:

- review fixtures when services change observability instrumentation
- generate fixtures from recorded OTLP exports (sanitized)
- version fixtures alongside service releases

## Collector version mismatch

Processor behavior can change between Collector versions.

Symptoms:

- CI passes with one Collector version while production runs another
- config keys or component semantics differ across releases

Mitigation:

- run `otel-policy-lab validate --collector-config <path> --otelcol-bin <pinned-binary>` in CI
- pin Collector version in future `RealCollectorRunner`
- record Collector version in JSON report metadata (planned)
- run policy tests in CI with the same Collector image used in production

## Incomplete runner simulation

The MVP `FixtureRunner` only simulates a subset of processor behavior inferred from config. It emits warnings for processors it cannot simulate or can only partially simulate.

Symptoms:

- policy passes against simulated output but fails against real Collector output
- complex processor chains are not fully represented
- attributes processor include/exclude matching is ignored by the simulator
- trace preservation appears successful even though sampling behavior was not exercised

Mitigation:

- treat runner warnings as confidence reducers
- treat MVP results as regression tests, not full integration proof
- prioritize `RealCollectorRunner` for high-risk changes
- keep policy assertions focused on governance outcomes that are easy to verify

## Fixture-local cardinality

Metric series checks count unique resource attributes plus datapoint label sets in the fixture. This is more accurate than raw datapoint counts, but it is still fixture-local.

Symptoms:

- CI passes while production has many more label values over time
- a short fixture underestimates high-cardinality attributes

Mitigation:

- use fixtures that represent realistic route/user/session diversity
- combine CI checks with production cardinality monitoring
- treat thresholds as guardrails, not cost forecasts

## Policy overconfidence

Passing all checks does not mean:

- observability cost is under control in production
- all secrets are prevented from export
- on-call will have sufficient logs and traces during incidents

Policies are guardrails. Operational validation still matters.

Mitigation:

- use policies as CI gates, not the only control
- monitor production cardinality, volume, and error trace rates
- review policy changes with platform and service owners

## Vacuous passes on empty signals

If a policy asserts `forbidden_*` checks for a signal but the evaluated output contains zero records of that signal, the forbidden check passes because there is nothing to match.

Example: a logs-only fixture with trace forbidden-attribute rules passes trace checks vacuously.

Mitigation:

- include representative records for every signal you assert against
- treat missing-signal passes as a fixture coverage smell, not proof of safety

## JSON report redaction

JSON reports never echo raw forbidden attribute values, log bodies, or full resource attribute maps on required-resource failures. Forbidden-data matches include metadata only (`value_length`, keys, patterns). Required-resource failures include missing key names and record indexes.

Terminal output may still name matched attribute keys in failure messages. Do not treat CI logs as secret-safe if key names themselves are sensitive.
