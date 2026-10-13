<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# opa

A Rego module decides. It allows the group `analysts` and adds a row filter on the tenant.

## Run

```bash
examples/opa/serve.sh
```

The script creates `data/shop.db` from `seed.sql` and starts Sluice on `http://localhost:8080`. Sluice loads `rego/main.rego`. Then run the steps from this folder.

| Step | Shows |
|---|---|
| `steps/01-analyst.sh` | An analyst of the tenant `acme` reads only acme rows. |
| `steps/02-guest.sh` | A subject in the group `guests` is denied. |

`expected/` holds the real output of each step. See [the examples guide](../README.md) for the layout.
