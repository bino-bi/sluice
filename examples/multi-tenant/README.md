<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# multi-tenant

Two tenants share one database. One `RowFilterPolicy` limits each API key to the rows of its tenant.

## Run

```bash
examples/multi-tenant/serve.sh
```

The script creates `data/shop.db` from `seed.sql` and starts Sluice on `http://localhost:8080`. Then run the steps from this folder, for example `bash steps/01-acme.sh`.

| Step | Shows |
|---|---|
| `steps/01-acme.sh` | The acme key reads only acme customers. |
| `steps/02-beta.sh` | The beta key runs the same SQL and reads only beta customers. |
| `steps/03-acme-asks-for-beta.sh` | Asking for another tenant's rows returns no rows. |
| `steps/04-acme-join.sh` | A join filters both tables. |
| `steps/05-acme-subquery.sh` | A subquery is filtered too. |

`expected/` holds the real output of each step. `tests/` holds a suite for `sluice policy test policies.d --tests tests`. See [the examples guide](../README.md) for the layout.

## Docker

```bash
python3 ../seed.py seed.sql data/shop.db
docker compose up
```

The compose file pulls the released image. `make docs-check` does not run it.
