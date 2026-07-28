<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Install

!!! warning "No published releases yet"
    Sluice is alpha, pre-`v0.1.0`. There are **no release binaries, no published container images, and no Helm chart** yet. The two supported paths are building from source and building the Docker image yourself via the example compose file.

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

## Docker

The repository root `Dockerfile` is a multi-stage build (Go builder → distroless `cc` runtime, non-root). The easiest way to use it is the `hello-sluice` example, whose compose file builds the image and mounts config, policies, and data:

```bash
cd examples/hello-sluice
sqlite3 data/shop.db < seed.sql
docker compose up --build
```

This starts Sluice with REST on `:8080` and the admin listener on `:9091`. The [Quickstart](quickstart.md) walks through what is inside.

To build a standalone image without compose:

```bash
docker build -t sluice:dev .
```

## Directory conventions

A Sluice deployment is two things on disk:

- **`sluice.yaml`** — the server configuration (listeners, DuckDB limits, audit sink, identity pepper, request limits). Passed with `sluice serve --config sluice.yaml`. A missing config file is fine — defaults apply; a malformed one is fatal. Any setting can also come from the environment with the `SLUICE_` prefix (`.` becomes `__`, e.g. `SLUICE_REST__LISTEN`). See the [configuration reference](../reference/configuration.md).
- **`policies.d/`** — one directory holding every declarative object: policies, `DataSource`, `SubjectBinding`, `AuditSink`. Loaded recursively (`*.yaml` / `*.yml`; dot-directories, `testdata`, and `tests` are skipped) and hot-reloaded on change. Point at it with `policies.directory` in `sluice.yaml` or `--policies-dir`.

!!! note "Empty means deny"
    An empty `policies.d/` is a valid configuration: it means *deny everything*. Access exists only where a policy grants it.

## Next

Continue with the [Quickstart](quickstart.md) to run the `hello-sluice` example end to end.
