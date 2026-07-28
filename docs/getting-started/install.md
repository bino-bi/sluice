<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Install

!!! warning "Alpha — release candidate"
    The current release is `v0.1.0-rc.2`. Binaries and container images are published, but the API surface and policy schema may still change before `v0.1.0`. Pin an exact version; do not track `latest`. There is still **no Helm chart** — see the [deployment guide](../operations/deployment.md) for running the image directly.

## Container image

Multi-arch (`linux/amd64`, `linux/arm64`), distroless, non-root:

```bash
docker pull ghcr.io/bino-bi/sluice:0.1.0-rc.2
docker run --rm ghcr.io/bino-bi/sluice:0.1.0-rc.2 version
```

Both images attach every datasource type without reaching out to `extensions.duckdb.org` at runtime: `amd64` has `httpfs`, `excel`, and `sqlite_scanner` compiled into the binary and pre-seeds the rest under `/home/nonroot/.duckdb/extensions`; `arm64` pre-seeds all six.

The image tag omits the leading `v` (`0.1.0-rc.2`, not `v0.1.0-rc.2`). Pre-releases are never tagged `latest`.

## Binary

Download the archive for your platform from the [releases page](https://github.com/bino-bi/sluice/releases). Available targets: `linux_amd64`, `linux_arm64`, `darwin_amd64`, and `darwin_arm64`.

!!! note "No Windows archive"
    Windows is not part of the release matrix. DuckDB's prebuilt Windows libraries do not link under the mingw cross-toolchain (they reference `pthread_*` symbols that the bindings never link against). A native `go build` on Windows works — CI covers it — so build from source there for now.

```bash
VERSION=0.1.0-rc.2
curl -fsSLO https://github.com/bino-bi/sluice/releases/download/v${VERSION}/sluice_${VERSION}_linux_amd64.tar.gz
tar -xzf sluice_${VERSION}_linux_amd64.tar.gz
./sluice version
```

The `linux_amd64` build is the static-extension flavour described under [Static DuckDB extensions](#static-duckdb-extensions); the others install `httpfs`/`excel`/`sqlite_scanner` at first attach.

!!! warning "Linux archives need glibc 2.38 or newer"
    The release binaries are cross-compiled on Ubuntu noble, so they require `GLIBC_2.38` and `GLIBCXX_3.4.32`. They run on Debian 13+, Ubuntu 24.04+, and Fedora 39+, but **not** on Debian 12, Ubuntu 22.04, or RHEL 9 — those fail at the dynamic linker with `version 'GLIBC_2.38' not found`. On an older distro, use the container image or build from source. The published container image already ships a matching runtime.

### Verifying a download

Every release ships `checksums.txt`, signed keylessly with [cosign](https://docs.sigstore.dev/), plus a CycloneDX SBOM per archive and SLSA v1.0 build provenance (`multiple.intoto.jsonl`).

```bash
# 1. checksum
curl -fsSLO https://github.com/bino-bi/sluice/releases/download/v${VERSION}/checksums.txt
sha256sum --check --ignore-missing checksums.txt

# 2. signature over the checksum file
curl -fsSLO https://github.com/bino-bi/sluice/releases/download/v${VERSION}/checksums.txt.sig
curl -fsSLO https://github.com/bino-bi/sluice/releases/download/v${VERSION}/checksums.txt.pem
cosign verify-blob \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity-regexp 'https://github.com/bino-bi/sluice/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

The container manifests are signed too:

```bash
cosign verify ghcr.io/bino-bi/sluice:${VERSION} \
  --certificate-identity-regexp 'https://github.com/bino-bi/sluice/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Build from source

Requirements:

- Go 1.25 or later.
- A C toolchain. Sluice links two cgo libraries — pg_query (the PostgreSQL parser) and DuckDB — so it cannot be built with `CGO_ENABLED=0`.

```bash
git clone https://github.com/bino-bi/sluice.git
cd sluice
make build
./bin/sluice version
```

`make build` produces `./bin/sluice` with version information stamped via ldflags; `sluice version` prints the tag, commit, build time, and parser backend. Run `make all` for the full fmt → vet → lint → test → build pipeline.

The first build compiles pg_query's vendored C sources and links DuckDB's prebuilt static library, so expect it to take several minutes; subsequent builds are cached.

### Static DuckDB extensions

`make build` produces a binary whose datasource drivers download DuckDB extensions (`httpfs`, `excel`, the sqlite/postgres/mysql scanners) from `extensions.duckdb.org` on first attach. To compile `httpfs`, `excel`, and `sqlite_scanner` into the binary instead — required for air-gapped operation with s3/http/sqlite sources — build the custom DuckDB bundle once and use the static target (the postgres/mysql scanners and `motherduck` cannot be embedded and always install at attach):

```bash
make libduckdb      # one-time: ~20-40 min, needs cmake + ninja + python3; cached under third_party/
make build-static
```

`make test-static` runs the test suite (plus a static-linking smoke test) against that bundle. The version of DuckDB to build is pinned in `scripts/DUCKDB_VERSION` and checked against `go.mod`.

### Building the container image yourself

The repository root `Dockerfile` is a multi-stage build (DuckDB bundle → Go builder → distroless `cc` runtime, non-root) that produces the same layout as the published image:

```bash
docker build -t sluice:dev .
```

Every example compose file honours a `SLUICE_IMAGE` override, so a local build drops straight in: `SLUICE_IMAGE=sluice:dev docker compose up`. See [Deployment](../operations/deployment.md) for the build arguments.

## Run the example stack

The `hello-sluice` example is a compose file that pulls the published image and mounts config, policies, and seeded data:

```bash
cd examples/hello-sluice
sqlite3 data/shop.db < seed.sql
docker compose up
```

This starts Sluice with REST on `:8080` and the admin listener on `:9091`. No Go toolchain and no build step — just the image pull. The [Quickstart](quickstart.md) walks through what is inside.

## Directory conventions

A Sluice deployment is two things on disk:

- **`sluice.yaml`** — the server configuration (listeners, DuckDB limits, audit sink, identity pepper, request limits). Passed with `sluice serve --config sluice.yaml`. A missing config file is fine — defaults apply; a malformed one is fatal. Any setting can also come from the environment with the `SLUICE_` prefix (`.` becomes `__`, e.g. `SLUICE_REST__LISTEN`). See the [configuration reference](../reference/configuration.md).
- **`policies.d/`** — one directory holding every declarative object: policies, `DataSource`, `SubjectBinding`, `AuditSink`. Loaded recursively (`*.yaml` / `*.yml`; dot-directories, `testdata`, and `tests` are skipped) and hot-reloaded on change. Point at it with `policies.directory` in `sluice.yaml` or `--policies-dir`.

!!! note "Empty means deny"
    An empty `policies.d/` is a valid configuration: it means *deny everything*. Access exists only where a policy grants it.

## Next

Continue with the [Quickstart](quickstart.md) to run the `hello-sluice` example end to end.
