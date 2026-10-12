<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# hello-sluice

The smallest complete setup: a SQLite database, an allow policy, a row filter and a column mask.

## Run

```bash
examples/hello-sluice/serve.sh
```

The script creates `data/shop.db` from `seed.sql` and starts Sluice on `http://localhost:8080`. Then run the steps from this folder, for example `bash steps/01-query.sh`.

| Step | Shows |
|---|---|
| `steps/01-query.sh` | A query returns only the rows of the caller's tenant, with `email` masked. |
| `steps/02-denied.sh` | A table that no policy allows is denied. |
| `steps/03-audit-verify.sh` | The audit log chain is intact. |
| `steps/04-policy-explain.sh` | `sluice policy explain` lists the policies that apply to the demo subject. |
| `steps/05-tenant-rows.sh` | A query without the masked column shows the row filter alone. |

`expected/` holds the real output of each step. See [the examples guide](../README.md) for the layout.

## Docker

```bash
python3 ../seed.py seed.sql data/shop.db
docker compose up
```

The compose file pulls the released image. `make docs-check` does not run it.
