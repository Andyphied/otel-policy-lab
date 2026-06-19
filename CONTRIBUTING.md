# Contributing

Thanks for helping improve `otel-policy-lab`.

## Development setup

```bash
go test ./...
make vet
make build
```

The project pins Go `1.22.4` and Collector `pdata v1.25.0` for reproducible OTLP JSON parsing. Use `GOTOOLCHAIN=local` if your global Go toolchain is newer.

## Pull request expectations

- Keep changes focused and include tests for behavior changes.
- Run `make test` and `make vet` before opening a PR.
- Update examples, docs, and golden fixtures when CLI or report output changes.
- Do not commit secrets, credentials, or real production telemetry.

## Golden tests

Terminal and JSON report output is locked with golden files under `internal/cli/testdata/` and `internal/report/testdata/`. Update goldens only when the change is intentional.

## Policy schema changes

Policy YAML uses strict field parsing. New fields must be added to the typed structs in `internal/policy/` and covered by parse/evaluator tests.

## Code style

Match existing package layout and naming. Prefer small, explicit functions over new abstractions unless they remove real duplication.

CI runs `golangci-lint` on pull requests. Fix lint findings in changed code when practical.
