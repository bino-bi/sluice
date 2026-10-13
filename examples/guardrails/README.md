<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# guardrails

Three `QueryRejectPolicy` objects and two `QueryRewritePolicy` objects for an agent that reads an `orders` table.

## Run

```bash
examples/guardrails/serve.sh
```

The script creates `data/shop.db` from `seed.sql` and starts Sluice on `http://localhost:8080`. Then run the steps from this folder, for example `bash steps/01-select-star.sh`.

| Step | Shows |
|---|---|
| `steps/01-select-star.sh` | A reject rule refuses `SELECT *`. |
| `steps/02-limit.sh` | A row limit cuts the result. |

`expected/` holds the real output of each step. `tests/` holds a suite for `sluice policy test policies.d --tests tests`. See [the examples guide](../README.md) for the layout.
