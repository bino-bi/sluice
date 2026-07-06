<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Cross-source joins

**Goal:** join transactional rows in Postgres against reference data in S3
Parquet in a single `SELECT`, with both catalogs governed by the same policy
set. Runnable version: `examples/cross-source-join/`.

## The data sources

Both catalogs are DuckDB attachments; the policy engine treats them
identically. Note the `allowedPaths` allow-list on the S3 side — the driver
refuses any object outside it, independently of what policy allows:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: DataSource
metadata:
  name: pg
spec:
  type: postgres
  connection: "postgres://sluice@postgres:5432/orders?sslmode=disable"
  credentialsRef: "secret://env/SLUICE_PG_PASSWORD"
  readonly: true
  schemas: ["public"]
---
apiVersion: sluice.bino.bi/v1alpha1
kind: DataSource
metadata:
  name: ref
spec:
  type: s3_parquet
  bucket: "ref-data"
  region: "us-east-1"
  endpoint: "http://minio:9000"
  allowedPaths:
    - "s3://ref-data/countries.parquet"   # allow-list: only this object is readable
  credentialsRef: "secret://env/SLUICE_S3_CREDS"
  readonly: true
  schemas: ["main"]
```

## The access policy

The allow policy names the tables on **both** catalogs — one clause per
catalog, since `catalogs`/`schemas`/`tables` constrain each other within a
clause:

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
          catalogs: ["pg"]
          schemas: ["public"]
          tables: ["orders"]
      - subjects:
          groups: ["analytics"]
        resources:
          catalogs: ["ref"]
          schemas: ["main"]
          tables: ["countries"]
```

!!! warning "Allow decisions are query-scoped"
    An allow policy admits the whole statement as soon as *any* referenced
    table matches one of its clauses — there is no per-table allow coverage
    check. Listing both tables explicitly is the right practice, but a broad
    clause (for example `tables: ["*"]`) on one catalog also admits joins that
    pull in tables from other catalogs. If catalogs must stay separated, add
    the reject policy below or set `limits.disableCrossCatalog: true`.

## Run and verify

The compose stack runs Postgres (seeded `orders`), MinIO (a one-shot job
writes `countries.parquet` into the `ref-data` bucket), and Sluice:

```bash
docker compose up --build
# wait for "seeded ref-data/countries.parquet" and the server start

curl -s -H "X-Api-Key: analyst.supersecret" -H "Content-Type: application/json" \
     -d '{"sql":"SELECT o.id, o.amount_cents, c.name AS country, c.region FROM pg.public.orders o JOIN ref.main.countries c ON c.code = o.country_code ORDER BY o.id"}' \
     http://localhost:8080/v1/query | jq -c '.rows'
# [[1,1999,"United States","Americas"],[2,4599,"Germany","EMEA"], ...]
```

A table outside the policy is denied as usual:

```bash
curl -s -H "X-Api-Key: analyst.supersecret" -H "Content-Type: application/json" \
     -d '{"sql":"SELECT usename FROM pg.public.pg_stat_activity"}' \
     http://localhost:8080/v1/query | jq '.code'
# "ACL_DENIED"
```

## Opting out of cross-catalog joins

Two mechanisms, from coarse to fine. Globally, in `server.yaml`:
`limits.disableCrossCatalog: true` (default `false`). Per subject or resource,
as a policy:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: QueryRejectPolicy
metadata:
  name: no-cross-catalog
  priority: 200
spec:
  match:
    any:
      - subjects:
          groups: ["analytics"]
  reject:
    rules:
      - name: single-catalog-only
        expression: "size(query.catalogs) > 1"
        message: "queries may not span more than one catalog"
        code: ACL_REJECTED
```

Verified as a policy test: the join above then expects `outcome: reject` with
`rejections: ["no-cross-catalog/single-catalog-only"]`, while single-catalog
queries still pass.

## Pitfall: wildcards across catalogs

`tables: ["*"]` in a clause without `catalogs`/`schemas` constraints matches a
table of that name in *every* attached catalog — including catalogs you attach
next quarter. When more than one DataSource is loaded, always pin `catalogs:`
(and usually `schemas:`) in each clause, and keep S3 `allowedPaths` narrow: a
`**` glob over a bucket prefix is convenient for partitioned layouts but makes
every future object under that prefix readable.

## See also

- [Data sources](../operations/data-sources.md) — all six drivers and their options.
- [Guardrails](../policies/guardrails.md) — QueryRejectPolicy and the CEL query variables.
- [Server configuration](../operations/server-config.md) — the `limits` block.
