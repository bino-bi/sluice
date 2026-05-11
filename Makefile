# SPDX-License-Identifier: AGPL-3.0-or-later

# Sluice Makefile — first implementation slice.
# Targets land incrementally; docs / release / integration / e2e are deferred.

GO           ?= go
GOLANGCI     ?= golangci-lint
BIN_DIR      ?= ./bin
BIN          ?= $(BIN_DIR)/sluice

VERSION      ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "v0.0.0-dev")
COMMIT       ?= $(shell git rev-parse --verify --short HEAD 2>/dev/null || echo "unknown")
COMMIT_FULL  ?= $(shell git rev-parse --verify HEAD 2>/dev/null || echo "unknown")
DIRTY        ?= $(shell test -n "$$(git status --porcelain 2>/dev/null)" && echo "true" || echo "false")
BUILD_TIME   ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PARSER       ?= pg_query

# ldflags populate internal/version at build time.
LDFLAGS := \
  -X github.com/bino-bi/sluice/internal/version.Version=$(VERSION) \
  -X github.com/bino-bi/sluice/internal/version.Commit=$(COMMIT) \
  -X github.com/bino-bi/sluice/internal/version.CommitFull=$(COMMIT_FULL) \
  -X github.com/bino-bi/sluice/internal/version.Dirty=$(DIRTY) \
  -X github.com/bino-bi/sluice/internal/version.BuildTime=$(BUILD_TIME) \
  -X github.com/bino-bi/sluice/internal/version.Parser=$(PARSER)

.PHONY: all build test test-integration test-fuzz bench coverage lint fmt vet tidy clean spdx \
        docs-generate docs-check docs-serve release-snapshot help

all: fmt vet lint test build

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/sluice

test:
	$(GO) test -race -short ./...

# Integration tests pull up testcontainers (postgres, mysql, minio). Runs
# only when the `integration` build tag is present so unit-test PRs stay
# fast. The nightly CI lane sets INTEGRATION=1.
test-integration:
	$(GO) test -race -tags=integration ./...

# Smoke-run every fuzz target for FUZZTIME seconds so PRs catch panics
# on newly-added fuzzers without running them at full nightly duration.
FUZZTIME ?= 5s
test-fuzz:
	$(GO) test -run=^$$ -fuzz=FuzzParse          -fuzztime=$(FUZZTIME) ./internal/pgquery/
	$(GO) test -run=^$$ -fuzz=FuzzRecordCanonical -fuzztime=$(FUZZTIME) ./internal/audit/
	$(GO) test -run=^$$ -fuzz=FuzzTemplate       -fuzztime=$(FUZZTIME) ./internal/policy/
	$(GO) test -run=^$$ -fuzz=FuzzRewrite        -fuzztime=$(FUZZTIME) ./internal/rewriter/
	$(GO) test -run=^$$ -fuzz=FuzzValidateArgs   -fuzztime=$(FUZZTIME) ./pkg/mask/

# Run every benchmark once for regression telemetry. CI uploads the output
# so `benchstat` can compare against the main branch baseline (plan 24 §10).
BENCHTIME ?= 1s
bench:
	$(GO) test -run=^$$ -bench=. -benchtime=$(BENCHTIME) -benchmem ./...

# Produce coverage.out and gate each policed package against the
# thresholds in plan/24-testing.md §11.
coverage:
	$(GO) test -race -short -coverprofile=coverage.out -covermode=atomic ./...
	$(GO) tool cover -func=coverage.out | tail -n 5
	@./scripts/check-coverage.sh coverage.out

lint: spdx
	$(GOLANGCI) run ./...

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

spdx:
	@./scripts/check-spdx.sh

# Regenerate auto-built reference pages under docs/reference/ from the
# Go source of truth. Each scripts/gen-*-docs.go is a //go:build ignore
# program run with `go run`. cli.md stays hand-maintained until the
# cobra construction is extracted from cmd/sluice (package main blocks
# reflection-based discovery from a script).
docs-generate:
	$(GO) run scripts/gen-errorcodes-docs.go -out docs/reference/error-codes.md
	$(GO) run scripts/gen-policy-docs.go -out docs/reference/policy-schema.md
	$(GO) run scripts/gen-metrics-docs.go -out docs/reference/metrics.md
	$(GO) run scripts/gen-config-docs.go -out docs/reference/configuration.md

# CI-side drift gate: re-runs every generator with -check and fails when
# the committed file differs from the generator's current output. Wired
# into docs-deploy.yaml so PRs that touch pkg/errors / apitypes / metric
# definitions / ServerConfig must regenerate.
docs-check:
	$(GO) run scripts/gen-errorcodes-docs.go -check -out docs/reference/error-codes.md
	$(GO) run scripts/gen-policy-docs.go -check -out docs/reference/policy-schema.md
	$(GO) run scripts/gen-metrics-docs.go -check -out docs/reference/metrics.md
	$(GO) run scripts/gen-config-docs.go -check -out docs/reference/configuration.md

# Serve the mkdocs site locally at http://127.0.0.1:8000 for writers.
# Requires `pip install mkdocs-material` — see docs-deploy.yaml for the
# full dep pin set.
docs-serve:
	@if ! command -v mkdocs >/dev/null 2>&1; then \
	  echo "mkdocs not installed — pip install 'mkdocs==1.6.*' 'mkdocs-material==9.5.*'"; \
	  exit 1; \
	fi
	mkdocs serve

# Run goreleaser in snapshot mode (no publishing) to exercise the full
# archive + docker + SBOM + sign pipeline locally. Requires goreleaser,
# docker buildx, cosign, and syft on PATH (or run via goreleaser-cross).
release-snapshot:
	goreleaser release --snapshot --clean --skip=publish

clean:
	rm -rf $(BIN_DIR) dist coverage.out coverage.html site

help:
	@echo "Targets:"
	@echo "  build             Build ./bin/sluice with version ldflags"
	@echo "  test              go test -race -short ./... (PR lane)"
	@echo "  test-integration  go test -tags=integration ./... (requires docker)"
	@echo "  test-fuzz         Smoke-run every fuzz target for FUZZTIME (default 5s)"
	@echo "  bench             Run every benchmark once with -benchmem"
	@echo "  coverage          go test -coverprofile + per-package threshold gate"
	@echo "  lint              golangci-lint + SPDX check"
	@echo "  fmt               go fmt ./..."
	@echo "  vet               go vet ./..."
	@echo "  tidy              go mod tidy"
	@echo "  spdx              scripts/check-spdx.sh"
	@echo "  docs-generate     regenerate auto-built docs/reference pages"
	@echo "  docs-serve        serve the mkdocs site locally at :8000"
	@echo "  release-snapshot  goreleaser snapshot build (no publish)"
	@echo "  clean             remove build artefacts"
	@echo "  all               fmt → vet → lint → test → build"
