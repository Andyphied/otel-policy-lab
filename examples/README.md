# Examples

This directory contains small, reviewable examples for the MVP.

## Files

- `collector.yaml`: representative Collector config with redaction, debug-log filtering, high-cardinality label deletion, and batching.
- `policy.yaml`: intentionally strict policy that fails because `http.server.duration` has more than 10,000 unique fixture-local series.
- `policy-pass.yaml`: same policy surface with a higher cardinality threshold, useful for demonstrating a successful CI run with warnings.
- `fixtures/checkout.otlp.json`: representative checkout telemetry fixture in OTLP JSON format.

## Failing Example

```sh
otel-policy-lab test \
  --collector-config ./examples/collector.yaml \
  --input ./examples/fixtures/checkout.otlp.json \
  --policy ./examples/policy.yaml
```

Expected result: exit code `1` because the metric cardinality policy fails.

## Passing Example

```sh
otel-policy-lab test \
  --collector-config ./examples/collector.yaml \
  --input ./examples/fixtures/checkout.otlp.json \
  --policy ./examples/policy-pass.yaml
```

Expected result: exit code `0` with warnings. The warnings are intentional: the fixture runner partially simulates some Collector behavior and does not simulate span dropping or sampling.
