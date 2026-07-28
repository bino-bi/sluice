# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Multi-stage build for Sluice. pg_query_go and go-duckdb both require cgo,
# so we build against a glibc-based runtime and ship a distroless cc image.
#
# The libduckdb stage compiles DuckDB with httpfs/excel/sqlite_scanner
# statically linked (scripts/build-libduckdb.sh), and the extensions
# stage pre-seeds the non-embeddable postgres_scanner/mysql_scanner/
# motherduck loadable extensions into the DuckDB extension directory —
# the image never downloads extensions at runtime. It builds
# for the container arch — on Apple Silicon `docker build` yields a static
# linux/arm64 dev image, a superset of the released linux/amd64 target.
# The layer is cached until scripts/DUCKDB_VERSION or the script changes
# (~30 min cold).
#
# Targets:
#   docker build -t sluice:dev .
#   docker build --build-arg VERSION=v0.1.0 -t sluice:v0.1.0 .

# ─── libduckdb (static extension bundle) ─────────────────────────────────
FROM golang:1.25-bookworm AS libduckdb

RUN apt-get update && apt-get install -y --no-install-recommends \
      cmake ninja-build python3 zip unzip pkg-config \
    && rm -rf /var/lib/apt/lists/*

# DuckDB's C++ compile units need ~1-2 GB each; unbounded ninja
# parallelism OOMs Docker Desktop's default ~8 GB VM. Raise with
# --build-arg LIBDUCKDB_JOBS=N when the daemon has more memory.
ARG LIBDUCKDB_JOBS=4
ENV CMAKE_BUILD_PARALLEL_LEVEL=${LIBDUCKDB_JOBS}

WORKDIR /src

# go.mod/go.sum feed the version drift check inside the script.
COPY go.mod go.sum ./
COPY scripts/DUCKDB_VERSION scripts/build-libduckdb.sh ./scripts/
RUN ./scripts/build-libduckdb.sh

# ─── extensions (loadable, pre-seeded for offline attach) ────────────────
# postgres_scanner/mysql_scanner/motherduck cannot be compiled in (see
# scripts/build-libduckdb.sh); fetch their signed loadable builds into
# the layout DuckDB's extension directory expects. Signatures are
# verified by DuckDB at LOAD time.
FROM golang:1.25-bookworm AS extensions

ARG TARGETARCH
WORKDIR /src
COPY scripts/DUCKDB_VERSION scripts/fetch-duckdb-extensions.sh ./scripts/
RUN ./scripts/fetch-duckdb-extensions.sh "linux_${TARGETARCH}" /out \
      postgres_scanner mysql_scanner motherduck

# ─── builder ─────────────────────────────────────────────────────────────
FROM golang:1.25-bookworm AS builder

ARG VERSION=dev
ARG COMMIT=unknown
ARG COMMIT_FULL=unknown
ARG BUILD_TIME=unknown
ARG PARSER=pg_query

WORKDIR /src

# Dependencies first for a warm layer cache.
COPY go.mod go.sum ./
RUN go mod download

# Sources.
COPY cmd     ./cmd
COPY internal ./internal
COPY pkg     ./pkg
COPY scripts ./scripts

COPY --from=libduckdb /src/third_party/libduckdb /src/third_party/libduckdb

# Build with ldflags matching the Makefile, linking the static DuckDB
# extension bundle (duckdb_use_static_lib).
ENV CGO_ENABLED=1
RUN CGO_CFLAGS="-DDUCKDB_STATIC_BUILD" \
    CGO_LDFLAGS="-L/src/third_party/libduckdb/linux_$(go env GOARCH) -lduckdb_bundle -lstdc++ -lm -ldl" \
    go build \
      -trimpath \
      -tags duckdb_use_static_lib \
      -ldflags "-s -w \
        -X github.com/bino-bi/sluice/internal/version.Version=${VERSION} \
        -X github.com/bino-bi/sluice/internal/version.Commit=${COMMIT} \
        -X github.com/bino-bi/sluice/internal/version.CommitFull=${COMMIT_FULL} \
        -X github.com/bino-bi/sluice/internal/version.BuildTime=${BUILD_TIME} \
        -X github.com/bino-bi/sluice/internal/version.Parser=${PARSER}" \
      -o /out/sluice \
      ./cmd/sluice

# ─── runtime ─────────────────────────────────────────────────────────────
# distroless cc bundles glibc + libgcc + libstdc++, which DuckDB's cgo
# bindings pull in at runtime. The :nonroot tag pins uid/gid 65532.
FROM gcr.io/distroless/cc-debian12:nonroot AS runtime

ARG VERSION=dev

LABEL org.opencontainers.image.title="sluice"
LABEL org.opencontainers.image.description="SQL access-control gateway"
LABEL org.opencontainers.image.source="https://github.com/bino-bi/sluice"
LABEL org.opencontainers.image.licenses="AGPL-3.0-or-later"
LABEL org.opencontainers.image.version="${VERSION}"

COPY --from=builder /out/sluice /usr/local/bin/sluice
# DuckDB resolves ~/.duckdb/extensions via $HOME (/home/nonroot here);
# drivers LOAD these without any network access (common.EnsureExtension).
COPY --from=extensions --chown=65532:65532 /out /home/nonroot/.duckdb/extensions

# Policy and audit directories are bind-mounted by the operator.
# /var/lib/sluice is a sensible default for audit persistence.
USER nonroot:nonroot
WORKDIR /var/lib/sluice

EXPOSE 8080 8081 9090

ENTRYPOINT ["/usr/local/bin/sluice"]
CMD ["serve"]
