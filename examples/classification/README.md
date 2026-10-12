<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# classification

A `DataClassification` tags columns and tables. A mask policy and a deny policy match on the tags.

## Run

```bash
examples/classification/serve.sh
```

The script creates `data/shop.db` from `seed.sql` and starts Sluice on `http://localhost:8080`. Then run the steps from this folder.

| Step | Shows |
|---|---|
| `steps/01-customers.sh` | `email` and `phone` have the tag `pii` and come back masked. |
| `steps/02-suppliers.sh` | `suppliers.email` has no tag and comes back as it is. |
| `steps/03-payroll.sh` | `payroll` has the tag `restricted`. The query is denied. |

`expected/` holds the real output of each step. `tests/` holds a suite for `sluice policy test policies.d --tests tests`. See [the examples guide](../README.md) for the layout.
