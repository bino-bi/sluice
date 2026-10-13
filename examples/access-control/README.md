<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# access-control

Two allow policies and one deny policy on a small shop database. Analysts read one table. Admins read every table except `salaries`.

## Run

```bash
examples/access-control/serve.sh
```

The script creates `data/shop.db` from `seed.sql` and starts Sluice on `http://localhost:8080`. Then run the steps from this folder, for example `bash steps/01-allowed-table.sh`.

| Step | Shows |
|---|---|
| `steps/01-allowed-table.sh` | An analyst reads the allowed table. |
| `steps/02-other-table.sh` | A table that no allow names is denied. |
| `steps/03-join.sh` | A join needs an allow for every table. |
| `steps/04-admin-salaries.sh` | A deny wins over an allow. |

`expected/` holds the real output of each step. `tests/` holds a suite for `sluice policy test policies.d --tests tests`. See [the examples guide](../README.md) for the layout.
