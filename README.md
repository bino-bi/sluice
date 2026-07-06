# Sluice

**A policy-enforcing SQL gateway between AI agents, BI tools, and your databases.**

[![License: AGPL-3.0-or-later](https://img.shields.io/badge/license-AGPL--3.0--or--later-blue)](LICENSE)
[![pkg/: Apache-2.0](https://img.shields.io/badge/pkg%2F-Apache--2.0-blue)](LICENSE-APACHE)
[![Docs: CC-BY-4.0](https://img.shields.io/badge/docs-CC--BY--4.0-blue)](LICENSE-CC-BY)

## What is Sluice?

Sluice parses every inbound SQL statement with the real PostgreSQL grammar (pg_query) and evaluates it against declarative, default-deny YAML policies — an empty policy directory denies everything. Policies rewrite the query in flight: row filters are injected as parameterized `WHERE` clauses, column masks replace expressions in the target list, and `LIMIT`, sampling, and timeouts are clamped before anything reaches a backend. The rewritten statement executes read-only through embedded DuckDB against attached catalogs (Postgres, MySQL, SQLite, S3 Parquet, DuckDB files, MotherDuck). Every request — allowed or denied — is appended to a hash-chained JSONL audit log that `sluice audit verify` can replay offline.

```
client (REST / MCP) ─▶ identify ─▶ parse ─▶ policy ─▶ rewrite ─▶ execute (DuckDB, read-only) ─▶ audit ─▶ rows
```

## Sixty-second example

Drop three policies into a `policies.d/` directory:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: allow-analytics
  priority: 100
spec:
  effect: allow
  match:
    any:
      - subjects:
          groups: ["analytics"]
        resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["customers", "orders"]
---
apiVersion: sluice.bino.bi/v1alpha1
kind: RowFilterPolicy
metadata:
  name: filter-tenant
  priority: 80
spec:
  match:
    any:
      - resources:
          tables: ["shop.main.customers", "shop.main.orders"]
  combine: restrictive
  filter:
    predicate:
      column: tenant_id
      op: Equals
      value: "{{ subject.tenantId }}"
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata:
  name: mask-email
  priority: 50
spec:
  match:
    any:
      - resources:
          tables: ["shop.main.customers"]
          columns: ["email"]
  mask:
    type: partial
    args: { showFirst: 1, showLast: 0 }
```

Start the server and query it (the runnable version — datasource, API-key binding, seed data — lives in [`examples/hello-sluice/`](examples/hello-sluice/README.md); it ships a `null` mask instead of the `partial` mask above, so swap in the mask shown here to reproduce this exact response):

```bash
sluice serve --config server.yaml --policies-dir policies.d

curl -s \
  -H "X-Api-Key: sl_demo_hello.world" \
  -H "Content-Type: application/json" \
  -d '{"sql":"SELECT id, email, tenant_id FROM shop.main.customers ORDER BY id"}' \
  http://localhost:8080/v1/query
```

```json
{
  "query_id": "01KWV0FD2DK4TRGZG9XCDJKSS1",
  "columns": ["id", "email", "tenant_id"],
  "rows": [
    [1, "a**************", "acme"],
    [2, "b************", "acme"]
  ],
  "row_count": 2,
  "truncated": false
}
```

The `customers` table holds rows for three tenants; the caller sees only their own, with emails masked — and every request, even the denied ones, lands in the hash-chained audit log.

## Highlights

- **11 policy kinds** — access gates, row filters, column masks, rejections, rewrites, approvals, datasources, subject bindings, audit sinks, data classifications, relationship policies.
- **Mask catalog** — SQL-stage `null`, `constant`, `partial`, `hash`, `regex`, `truncate` plus post-query `hmac_sha256`, format-preserving encryption (FF1), `jitter`, and deterministic `fake` data.
- **CEL** — conditions on any enforcement policy, reject rules, and a safe row-filter expression subset.
- **Human approval workflow** — `ApprovalPolicy` parks sensitive queries, notifies via webhooks, and issues single-use grants.
- **Pluggable engines** — embedded OPA (Rego) and OpenFGA-backed ReBAC compose with the YAML engine.
- **Per-subject rate limits and daily budgets** — token buckets plus CPU-seconds/rows-per-day quotas.
- **Hot reload** — fsnotify, SIGHUP, or `POST /admin/reload`; bad snapshots are rejected, the old one stays live.
- **Policy test harness** — `sluice policy test` runs YAML fixture suites against a policy directory.
- **Tamper-evident audit** — hash-chained JSONL with `sluice audit verify`.
- **MCP server** — 9 tools (`execute_sql`, `explain_access`, `whoami`, …) over stdio or Streamable HTTP.
- **6 datasource drivers** — Postgres, MySQL, SQLite, S3 Parquet, DuckDB file, MotherDuck.

## Install

Build from source (Go 1.25+; CGO is required because pg_query and DuckDB are C libraries):

```bash
git clone https://github.com/bino-bi/sluice.git
cd sluice
make build
./bin/sluice version
```

Or run the containerized example — the compose file builds the image from the repo-root `Dockerfile`:

```bash
cd examples/hello-sluice
sqlite3 data/shop.db < seed.sql
docker compose up --build
```

There are no published release binaries or images yet; building from source (or docker compose) is the supported path.

## Documentation

Full documentation: **<https://bino-bi.github.io/sluice/>**

- [Getting started](docs/getting-started/index.md) — install, quickstart, first query.
- [Policies](docs/policies/index.md) — the policy DSL, matching, masks, approvals.
- [Architecture](docs/architecture/index.md) — request lifecycle and security model.
- [Reference](docs/reference/index.md) — policy schema, configuration, REST API, MCP tools, error codes.

## Project layout

- `cmd/` — CLI and composition root (AGPL-3.0-or-later).
- `pkg/` — public API packages: error catalog, policy types, mask providers, driver interface (Apache-2.0).
- `internal/` — the gateway itself (AGPL-3.0-or-later).
- `docs/` — documentation site source (CC-BY-4.0).
- `examples/` — runnable end-to-end examples.

## Status

Sluice is **alpha**, pre-`v0.1.0`. The end-to-end query path — identity, parse, policy, rewrite, execute, audit — works across REST, MCP, and the admin port, but interfaces may still change. See [ROADMAP.md](ROADMAP.md).

## License

| Scope | License | File |
| --- | --- | --- |
| `pkg/`, `sdk/` | Apache-2.0 | [LICENSE-APACHE](LICENSE-APACHE) |
| Everything else (code) | AGPL-3.0-or-later | [LICENSE](LICENSE) |
| `docs/` | CC-BY-4.0 | [LICENSE-CC-BY](LICENSE-CC-BY) |

Every source file carries an SPDX header; see [NOTICE](NOTICE) for attribution.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Contributions are accepted under the DCO (`git commit -s`); run `make all` (fmt → vet → lint → test → build) before opening a PR.
