<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Quickstart

The fastest way to see Sluice in action is the `hello-sluice` example,
which bundles a single-node SQLite catalog with one row filter and one
column mask.

```bash
git clone https://github.com/bino-bi/sluice.git
cd sluice/examples/hello-sluice
sqlite3 data/shop.db < seed.sql
docker compose up
```

This launches Sluice on `:8080` (REST) and `:9091` (admin/metrics).
Three tenants' worth of orders are seeded; the bundled policy only
exposes the `acme` tenant and masks `customer_email`.

The full README next to the example walks the request-by-request flow
and shows the expected JSON response.

## What's inside

- `server.yaml` — REST on 8080, admin on 9091, audit under `./data/audit`.
- `policies.d/` — one `DataSource`, one `SubjectBinding`, one
  `SqlAccessPolicy`, one `RowFilterPolicy`, one `ColumnMaskPolicy`.
- `seed.sql` — 5 customers, 6 orders, 3 tenants.
- `docker-compose.yaml` — builds the repo-root Dockerfile and bind-mounts
  the example directory.

Continue with [First query](first-query.md) to see the policy take
effect end-to-end.
