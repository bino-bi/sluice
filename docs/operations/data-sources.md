<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Data sources

Each `DataSource` manifest attaches one backend as a DuckDB catalog named
after `metadata.name`. Queries and policy selectors address tables with
the fully qualified `catalog.schema.table` form — a source named `shop`
exposing SQLite's `main` schema yields `shop.main.customers`.

Manifests are plain YAML objects. The shipped examples keep them next to
the policies in `policies.d`; the server config also offers a dedicated
`datasources.directory` (default `./datasources.d`). Changing a
`DataSource` requires a restart — see
[Configuration reload](hot-reload.md).

## Drivers

### postgres

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: DataSource
metadata:
  name: pg
spec:
  type: postgres
  connection: "postgres://sluice@postgres:5432/orders?sslmode=require"
  credentialsRef: "secret://env/SLUICE_PG_PASSWORD"
  readonly: true
  schemas: ["public"]
```

### mysql

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: DataSource
metadata:
  name: crm
spec:
  type: mysql
  connection: "mysql://sluice@mysql.internal:3306/crm"   # or mariadb://
  credentialsRef: "secret://env/SLUICE_MYSQL_PASSWORD"
  readonly: true
  schemas: ["crm"]
```

### sqlite

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: DataSource
metadata:
  name: shop
spec:
  type: sqlite
  path: /data/shop.db
  readonly: true
  schemas: ["main"]
```

### duckdb_file

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: DataSource
metadata:
  name: metrics
spec:
  type: duckdb_file
  path: /data/metrics.duckdb
  readonly: true
```

### s3_parquet

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: DataSource
metadata:
  name: ref
spec:
  type: s3_parquet
  bucket: "ref-data"
  region: "us-east-1"
  endpoint: "http://minio:9000"          # optional, for S3-compatible stores
  allowedPaths:                          # driver-level allow-list
    - "s3://ref-data/countries.parquet"
  credentialsRef: "secret://env/SLUICE_S3_CREDS"
  readonly: true
  schemas: ["main"]
```

### motherduck

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: DataSource
metadata:
  name: warehouse
spec:
  type: motherduck
  database: analytics
  tokenRef: "secret://env/SLUICE_MOTHERDUCK_TOKEN"
  readonly: true
```

## Common fields

| Field | Meaning |
| ----- | ------- |
| `readonly` | Declares the source read-only. Catalogs are attached `READ_ONLY` regardless. |
| `schemas` / `tables` | Restrict which schemas/tables the catalog exposes. |
| `attachMode` | `readonly` (current behavior) or `readwrite` — the latter is declared for a future release; every attach is read-only today. |
| `healthCheck` | `{query, interval}` — per-source liveness probe. The health loop ticks every 30 s by default; `-1` disables it. |

Credentials resolve through [`secret://` references](server-config.md) into
DuckDB secrets — they never appear in logs or audit records.

## The read-only guarantee

Sluice enforces a statement allowlist (`SELECT`, `EXPLAIN`, `SET`, `SHOW`,
`PRAGMA`) *before* any query reaches a source: `INSERT`/`UPDATE`/`DELETE`
return `ACL_REJECTED`, and `COPY`, DDL, `ATTACH`, `LOAD`, and `INSTALL`
return `ERR_UNSUPPORTED_SYNTAX` — regardless of what the database grants
allow. Still provision read-only database credentials for every
`connection`: defense in depth costs nothing.

## Checking sources

```bash
sluice datasource check --dir ./policies.d        # alias: sluice ds check
```

`check` is the spec-level CI gate: it loads the manifests and verifies each
`spec.type` has a registered driver and the document is structurally valid
(exit `2` when any source fails). It does **not** open connections — live
attach and the periodic health probe happen in `sluice serve`, and
`GET /v1/ready` reports per-source health.

For editor and CI schema validation of the manifests themselves:

```bash
sluice schema export --kind DataSource > datasource.schema.json
```

## Cross-catalog joins

Because every source is a catalog in the same DuckDB instance, one `SELECT`
can join PostgreSQL against S3 Parquet. Set
`limits.disableCrossCatalog: true` to forbid multi-catalog queries. See the
[cross-source joins cookbook](../cookbook/cross-source-joins.md) for a
worked example.
