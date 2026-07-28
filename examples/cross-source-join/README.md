<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# cross-source-join

Join transactional data in Postgres against reference data in S3
Parquet — in a single SELECT — with the policy engine treating the
two catalogs identically. DuckDB does the heavy lifting; Sluice
supplies the access-control layer on top.

## What this demonstrates

- **Cross-catalog joins work by default.** Sluice allows them unless
  you opt out with `limits.disableCrossCatalog: true` in
  `server.yaml`.
- **Per-table access control.** Granting `pg.public.orders` does not
  grant `ref.main.countries` — both tables must appear in the
  `SqlAccessPolicy` or the whole query is denied. This keeps joins
  explicit: you decide every cell of the access matrix.
- **Allow-listed S3 paths.** The `s3` driver only reads files
  listed in `allowedPaths`. Even if policy allows the catalog, the
  driver refuses any other bucket/key.

## Layout

```
cross-source-join/
├── README.md
├── docker-compose.yaml         — Postgres + MinIO + seed job + Sluice
├── server.yaml
├── seed/
│   ├── postgres.sql            — seeded into the Postgres `orders` db
│   └── README.md               — how to seed the parquet out-of-band
├── policies.d/
│   ├── datasource-pg.yaml      — Postgres connection
│   ├── datasource-s3.yaml      — S3 files with allowedPaths
│   ├── binding-apikey.yaml
│   └── allow-analytics.yaml    — SqlAccessPolicy lists BOTH tables
└── data/
```

## Run it

```bash
docker compose up
# wait for "seeded ref-data/countries.parquet" and "sluice: starting"
```

Sluice comes from the published image `ghcr.io/bino-bi/sluice:0.1.0-rc.1`; Postgres and MinIO come from their own upstream images. To run your own Sluice build instead: `docker build -t sluice:dev ../.. && SLUICE_IMAGE=sluice:dev docker compose up`.

## Run a join query

```bash
curl -s -H "X-Api-Key: sl_demo_analyst.supersecret" \
     -H "Content-Type: application/json" \
     -d @- http://localhost:8080/v1/query <<'JSON' | jq .
{
  "sql": "SELECT o.id, o.amount_cents, c.name AS country, c.region FROM pg.public.orders o JOIN ref.main.countries c ON c.code = o.country_code ORDER BY o.id"
}
JSON
```

Expected shape:

```json
{
  "columns": ["id","amount_cents","country","region"],
  "rows": [
    [1,  1999, "United States", "Americas"],
    [2,  4599, "Germany",       "EMEA"],
    [3,  7800, "France",        "EMEA"],
    [4,   250, "United States", "Americas"],
    [5, 32000, "Japan",         "APAC"]
  ],
  "row_count": 5
}
```

## Attempting a denied table

```bash
curl -s -H "X-Api-Key: sl_demo_analyst.supersecret" \
     -H "Content-Type: application/json" \
     -d '{"sql":"SELECT * FROM pg.public.pg_stat_activity"}' \
     http://localhost:8080/v1/query | jq .
# {"code":"ACL_DENIED","message":"no SqlAccessPolicy matched (default-deny)",...}
```

## Not for production

- The MinIO seeding service bakes credentials into the compose file
  for reproducibility. Real deployments point the S3 driver at a
  keyless IAM role (EKS/Pods) or a scoped access key rotated weekly.
- `sslmode=disable` on the Postgres connection is a demo shortcut.
  Use `sslmode=verify-full` against a real cluster.
- `countries.parquet` is a 4-row static fixture. Production reference
  data usually lives in a partitioned layout
  (`s3://ref-data/countries/dt=2026-04/*.parquet`) and `allowedPaths`
  should use a `**` glob in that case.
