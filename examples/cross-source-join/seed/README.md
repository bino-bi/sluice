<!-- SPDX-License-Identifier: CC-BY-4.0 -->

The `countries.parquet` reference data lives in the `minio` bucket
`ref-data` under key `countries.parquet`. Because generating Parquet
from Docker requires duckdb at seed time, the compose stack uses a
`duckdb-seed` one-shot service that produces the file on startup.

If you want to prepopulate the bucket out-of-band:

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

mc cp countries.parquet minio/ref-data/countries.parquet
```
