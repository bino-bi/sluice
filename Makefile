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

# Static DuckDB extension bundle (httpfs, excel, sqlite_scanner
# compiled into the binary; postgres/mysql scanners and motherduck
# cannot be embedded — see scripts/build-libduckdb.sh). Built once by
# `make libduckdb` (~20-40 min cold, cached under third_party/
# thereafter); consumed by build-static / test-static via go-duckdb's
# duckdb_use_static_lib tag. cgo reads CGO_CFLAGS (not CPPFLAGS) for
# -DDUCKDB_STATIC_BUILD.
DUCKDB_VERSION := $(shell cat scripts/DUCKDB_VERSION)
GOOS_HOST      := $(shell $(GO) env GOOS)
LIBDUCKDB_DIR  := $(CURDIR)/third_party/libduckdb/$(GOOS_HOST)_$(shell $(GO) env GOARCH)
# darwin: vcpkg's curl needs SystemConfiguration (proxy lookup) and
# minizip-ng links Apple's libcompression.
STATIC_EXT_LIBS_darwin := -lduckdb_bundle -lc++ -lcompression -framework SystemConfiguration
STATIC_EXT_LIBS_linux  := -lduckdb_bundle -lstdc++ -lm -ldl
STATIC_GO_ENV := CGO_ENABLED=1 CGO_CFLAGS="-DDUCKDB_STATIC_BUILD" \
                 CGO_LDFLAGS="-L$(LIBDUCKDB_DIR) $(STATIC_EXT_LIBS_$(GOOS_HOST))"

# ldflags populate internal/version at build time.
LDFLAGS := \
  -X github.com/bino-bi/sluice/internal/version.Version=$(VERSION) \
  -X github.com/bino-bi/sluice/internal/version.Commit=$(COMMIT) \
  -X github.com/bino-bi/sluice/internal/version.CommitFull=$(COMMIT_FULL) \
  -X github.com/bino-bi/sluice/internal/version.Dirty=$(DIRTY) \
  -X github.com/bino-bi/sluice/internal/version.BuildTime=$(BUILD_TIME) \
  -X github.com/bino-bi/sluice/internal/version.Parser=$(PARSER)

.PHONY: all build build-static libduckdb duckdb-extensions test test-integration test-static \
        test-fuzz bench coverage lint fmt vet tidy clean clean-libduckdb spdx \
        docs-generate docs-check docs-serve release-snapshot help

all: fmt vet lint test build

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/sluice

# Build (or reuse) the DuckDB static extension bundle for the host
# platform. Idempotent: a cached third_party/libduckdb/<os>_<arch>/
# bundle is a fast no-op; only a scripts/DUCKDB_VERSION bump rebuilds.
libduckdb:
	@./scripts/build-libduckdb.sh

# Like `build`, but with httpfs/excel/sqlite_scanner compiled in — no
# extensions.duckdb.org download at runtime for s3/http/sqlite sources.
build-static: libduckdb
	@mkdir -p $(BIN_DIR)
	$(STATIC_GO_ENV) $(GO) build -trimpath -tags duckdb_use_static_lib \
	  -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/sluice

test:
	$(GO) test -race -short ./...

# Integration tests pull up testcontainers (postgres, mysql, minio). Runs
# only when the `integration` build tag is present so unit-test PRs stay
# fast. The nightly CI lane sets INTEGRATION=1.
test-integration:
	$(GO) test -race -tags=integration ./...

# Unit tests against the static extension bundle, including the
# duckdb_use_static_lib-tagged smoke test in internal/executor.
test-static: libduckdb
	$(STATIC_GO_ENV) $(GO) test -race -short -tags duckdb_use_static_lib ./...

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

# Fetch the loadable DuckDB extensions the container images pre-seed
# (goreleaser extra_files). Idempotent; keyed on scripts/DUCKDB_VERSION.
duckdb-extensions:
	@./scripts/fetch-duckdb-extensions.sh linux_amd64 third_party/duckdb-extensions/amd64 \
	  postgres_scanner mysql_scanner motherduck
	@./scripts/fetch-duckdb-extensions.sh linux_arm64 third_party/duckdb-extensions/arm64 \
	  httpfs excel sqlite_scanner postgres_scanner mysql_scanner motherduck

# Run goreleaser in snapshot mode (no publishing) to exercise the full
# archive + docker + SBOM + sign pipeline locally. Requires goreleaser,
# docker buildx, cosign, and syft on PATH (or run via goreleaser-cross),
# plus LIBDUCKDB_DIR pointing at a linux/amd64 static bundle for the
# sluice-static build (see scripts/build-libduckdb.sh).
release-snapshot: duckdb-extensions
	goreleaser release --snapshot --clean --skip=publish

# Deliberately keeps third_party/libduckdb (a ~40 min rebuild); use
# clean-libduckdb to drop the cached bundle and DuckDB checkout.
clean:
	rm -rf $(BIN_DIR) dist coverage.out coverage.html site

clean-libduckdb:
	rm -rf third_party/libduckdb

help:
	@echo "Targets:"
	@echo "  build             Build ./bin/sluice with version ldflags"
	@echo "  build-static      Build with DuckDB extensions compiled in (needs libduckdb)"
	@echo "  libduckdb         Build/cache the DuckDB static bundle (~20-40 min cold)"
	@echo "  duckdb-extensions Fetch loadable extensions the container images pre-seed"
	@echo "  test              go test -race -short ./... (PR lane)"
	@echo "  test-integration  go test -tags=integration ./... (requires docker)"
	@echo "  test-static       go test with the static extension bundle (needs libduckdb)"
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
	@echo "  clean             remove build artefacts (keeps third_party/libduckdb)"
	@echo "  clean-libduckdb   remove the cached DuckDB static bundle + checkout"
	@echo "  all               fmt → vet → lint → test → build"
