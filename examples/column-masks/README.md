<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# column-masks

One `employees` table with a `ColumnMaskPolicy` for every mask type.

## Run

```bash
examples/column-masks/serve.sh
```

The script creates `data/hr.db` from `seed.sql` and starts Sluice on `http://localhost:8080`. Then run the steps from this folder, for example `bash steps/01-phone.sh`.

| Step | Shows |
|---|---|
| `steps/01-phone.sh` | A `partial` mask on one column. |
| `steps/02-all-masks.sh` | One row with every mask type side by side. |
| `steps/03-post-query-mask-in-where.sh` | A post-query masked column is refused in `WHERE`. |

| Suite | Shows |
|---|---|
| `tests/masks.yaml` | Which mask each column gets, and where the mask runs. |
| `tests/refused.yaml` | What Sluice refuses on a table with a post-query mask. |

`expected/` holds the real output of each step. See [the examples guide](../README.md) for the layout.
