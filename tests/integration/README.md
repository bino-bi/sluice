<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# Integration tests

Every test in this directory carries the `//go:build integration` tag
so it is skipped by the default `make test` lane. Run locally with:

```bash
make test-integration
```

…which invokes `go test -race -tags=integration ./...`. The nightly
CI workflow (`.github/workflows/nightly.yaml`) wires this lane into a
matrix of service containers (Postgres 16, MySQL 8, MinIO, and a
sqlite-file fixture) so every driver gets exercised against the real
backend — not a mock.

## What belongs here

- Driver attach + information_schema introspection against a live
  backend (not ` testfakes`).
- End-to-end round trips through `queryservice.Service` with a real
  DuckDB pool, real policy engine, real audit sink.
- Cross-source joins (pg + s3) once the S3 driver is exercised in
  CI.

## What does NOT belong here

- Anything that can be written with `testfakes` + mocks — that stays
  under the package the behaviour belongs to.
- Docker-compose scenarios — those live under `tests/e2e/` because
  they're exercising a full stack, not a single package.
