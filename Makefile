GO ?= go
# Static binaries for simple CI and GitHub Action distribution.
CGO_ENABLED ?= 0
VERSION ?= dev

.PHONY: test vet build example validate tidy

test:
	CGO_ENABLED=$(CGO_ENABLED) GOTOOLCHAIN=local $(GO) test ./...

vet:
	CGO_ENABLED=$(CGO_ENABLED) GOTOOLCHAIN=local $(GO) vet ./...

build:
	CGO_ENABLED=$(CGO_ENABLED) GOTOOLCHAIN=local $(GO) build -ldflags "-X main.version=$(VERSION)" -o otel-policy-lab ./cmd/otel-policy-lab

example:
	CGO_ENABLED=$(CGO_ENABLED) GOTOOLCHAIN=local $(GO) run ./cmd/otel-policy-lab test \
		--collector-config ./examples/collector.yaml \
		--input ./examples/fixtures/checkout.otlp.json \
		--policy ./examples/policy.yaml \
		--report ./report.json

validate:
	CGO_ENABLED=$(CGO_ENABLED) GOTOOLCHAIN=local $(GO) run ./cmd/otel-policy-lab validate \
		--collector-config ./examples/collector.yaml

tidy:
	GOTOOLCHAIN=local $(GO) mod tidy
