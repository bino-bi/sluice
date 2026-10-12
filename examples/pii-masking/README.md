<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# pii-masking

Three `ColumnMaskPolicy` objects mask `email`, `phone` and `ssn` in one table.

## Run

```bash
examples/pii-masking/serve.sh
```

The script creates `data/shop.db` from `seed.sql` and starts Sluice on `http://localhost:8080`. Then run the steps from this folder, for example `bash steps/01-masked.sh`.

| Step | Shows |
|---|---|
| `steps/01-masked.sh` | The three columns come back masked. `birth_date` is not masked. |
| `steps/02-filter-on-masked-column.sh` | A filter on a masked column sees the masked value, so it finds nothing. |
| `steps/03-group-by-masked-column.sh` | Grouping by a masked column groups the masked value. |

`expected/` holds the real output of each step. `tests/` holds a suite for `sluice policy test policies.d --tests tests`. See [the examples guide](../README.md) for the layout.

## Docker

```bash
python3 ../seed.py seed.sql data/shop.db
docker compose up
```

The compose file pulls the released image. `make docs-check` does not run it.
