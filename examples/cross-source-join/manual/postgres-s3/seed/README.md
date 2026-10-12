<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# Seed data

`postgres.sql` holds the orders. The `postgres` service loads it at first start.

The country list is the object `countries.parquet` in the MinIO bucket `ref-data`. The `seed-parquet` service in `../docker-compose.yaml` writes it at startup.

To write the object by hand, use the `duckdb` CLI and the MinIO client `mc`:

```bash
duckdb :memory: <<'SQL'
COPY (
  SELECT * FROM (VALUES
    ('US','United States','Americas'),
    ('DE','Germany','EMEA'),
    ('FR','France','EMEA'),
    ('JP','Japan','APAC')
  ) AS t(code, name, region)
) TO 'countries.parquet' (FORMAT PARQUET);
SQL

mc alias set local http://localhost:9000 sluice-demo demo-s3-password
mc cp countries.parquet local/ref-data/countries.parquet
```
