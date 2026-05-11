# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Multi-stage build for Sluice. pg_query_go and go-duckdb both require cgo,
# so we build against a glibc-based runtime and ship a distroless cc image.
#
# Targets:
#   docker build -t sluice:dev .
#   docker build --build-arg VERSION=v0.1.0 -t sluice:v0.1.0 .

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

# Build with ldflags matching the Makefile.
ENV CGO_ENABLED=1
RUN go build \
      -trimpath \
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

# Policy and audit directories are bind-mounted by the operator.
# /var/lib/sluice is a sensible default for audit persistence.
USER nonroot:nonroot
WORKDIR /var/lib/sluice

EXPOSE 8080 8081 9090

ENTRYPOINT ["/usr/local/bin/sluice"]
CMD ["serve"]
