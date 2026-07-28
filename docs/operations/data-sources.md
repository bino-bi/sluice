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

### s3

Exposes Parquet, CSV, JSON, and Excel (`.xlsx`) files on S3-compatible
object storage as read-only views — one view per `allowedPaths` entry.
The format is inferred per path from the file extension
(`.parquet`, `.csv`, `.json`/`.ndjson`/`.jsonl`, `.xlsx`); paths without
a recognizable extension (e.g. `raw/**`) require an explicit
`format:`, which then applies to every path in the manifest. Globs are
fine for parquet/csv/json but not for xlsx (`read_xlsx` opens a single
workbook).

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: DataSource
metadata:
  name: ref
spec:
  type: s3
  bucket: "ref-data"
  region: "us-east-1"
  endpoint: "http://minio:9000"          # optional, for S3-compatible stores
  allowedPaths:                          # driver-level allow-list
    - "s3://ref-data/countries.csv"      # → view ref.main.countries_csv
    - "s3://ref-data/facts/*.parquet"    # → view ref.main.facts___parquet
    - "s3://ref-data/plan.xlsx"          # → view ref.main.plan_xlsx
  credentialsRef: "secret://env/SLUICE_S3_CREDS"
  readonly: true
  schemas: ["main"]
```

```yaml
# Extension-less paths need the format pinned explicitly:
spec:
  type: s3
  bucket: "lake"
  region: "eu-central-1"
  format: parquet                        # applies to all allowedPaths
  allowedPaths:
    - "s3://lake/raw/**"
```

> Renamed from `s3_parquet`. Old manifests fail validation with a
> pointer at the rename; `type: s3` with the same fields (plus
> `format: parquet` for extension-less paths) is a drop-in replacement.

### http

Exposes remote files served over HTTP(S) as read-only views — one view
per URL. The `urls` list is the allow-list: no globs, every file is one
explicit URL, and nothing else on those hosts becomes readable. Format
inference works as for `s3` (query strings, e.g. pre-signed URLs, do
not defeat it); `format:` pins it when the URL has no telling extension.

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: DataSource
metadata:
  name: web
spec:
  type: http
  urls:
    - "https://data.example.com/exports/countries.csv"
    - "https://data.example.com/exports/facts.parquet"
    - "https://api.example.com/v1/report.json"
  headersRef: "secret://env/SLUICE_HTTP_HEADERS"   # optional, for authenticated endpoints
  readonly: true
```

`headersRef` resolves to a JSON object of header name → value (e.g.
`{"Authorization": "Bearer …"}`). The headers are installed as one
DuckDB HTTP secret per URL origin, scoped to that origin — they are
never sent to other hosts.

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

### Extension provisioning

Builds made with `make build-static`, the repository `Dockerfile`, and the
released linux/amd64 binary and `-amd64` container image have DuckDB's
`httpfs`, `excel`, and `sqlite_scanner` extensions **compiled into the
binary**. Three extensions cannot be embedded: `motherduck`
(closed-source), `mysql_scanner` (upstream duckdb-mysql supports loading
only, not static linking), and `postgres_scanner` (its vendored libpq
collides with the pg_query parser Sluice links).

**Container images pre-seed those three as loadable files** under
`/home/nonroot/.duckdb/extensions` (all six for the `-arm64` image, which
uses the prebuilt bindings without the compiled-in trio), so every
datasource type attaches without touching `extensions.duckdb.org` —
air-gap ready as shipped. Drivers load pre-seeded files via a bare
`LOAD`, and DuckDB verifies the extension signature at load time.

Host builds (`make build`/`build-static` outside a container, the
released darwin/windows/linux-arm64 binaries) still install
non-embedded extensions on first attach, downloading unless already
cached in `~/.duckdb`. Air-gapped hosts can pre-seed the same way:
`scripts/fetch-duckdb-extensions.sh <platform> ~/.duckdb/extensions
<extension>...` on a connected machine, then copy the directory over.

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

Readiness is fail-closed: every source starts unhealthy, one sweep fires
immediately at boot, then every `healthCheck.interval` (default 30 s). A
source that dies mid-run flips unhealthy within one interval, `/v1/ready`
turns `503`, and queries touching it return `ERR_DATASOURCE_UNAVAILABLE`
instead of `ERR_INTERNAL`.

For editor and CI schema validation of the manifests themselves:

```bash
sluice schema export --kind DataSource > datasource.schema.json
```

## Cross-catalog joins

Because every source is a catalog in the same DuckDB instance, one `SELECT`
can join PostgreSQL against files on S3. Set
`limits.disableCrossCatalog: true` to forbid multi-catalog queries. See the
[cross-source joins cookbook](../cookbook/cross-source-joins.md) for a
worked example.
