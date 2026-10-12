<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# cross-source-join

One `SELECT` joins a SQLite table with a table in a DuckDB file. Sluice checks access for each table.

## Run

```bash
examples/cross-source-join/serve.sh
```

The script creates `data/shop.db` from `seed.sql` and `data/ref.duckdb` from `seed-ref.sql`, then starts Sluice on `http://localhost:8080`. It needs Go to write the DuckDB file. Run the steps from this folder, for example `bash steps/01-join.sh`.

| Step | Shows |
|---|---|
| `steps/01-join.sh` | The analyst key joins `shop.main.orders` with `ref.main.countries`. |
| `steps/02-join-denied.sh` | The intern key is denied the same join. |
| `steps/03-countries-only.sh` | The intern key reads `countries`, its only allowed table. |
| `steps/04-join-masked.sh` | The mask on `customer_id` still applies inside the join. |
| `steps/05-validate-manual.sh` | The policy files in `manual/postgres-s3/` are valid. |

`policies.d/` holds the data sources, keys, allow policies and the mask. `expected/` holds the real output of each step. `tests/` holds a suite for `sluice policy test policies.d --tests tests`. See [the examples guide](../README.md) for the layout.

## Postgres and S3

`manual/postgres-s3/` holds the same join on Postgres and S3 Parquet for Docker Compose. `make docs-check` does not run it.
