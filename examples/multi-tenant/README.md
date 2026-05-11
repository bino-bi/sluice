<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# multi-tenant

A single Sluice deployment, one shared catalog (`customers` + `orders`),
three tenants' worth of data in the underlying table — and two API keys
that each see only their tenant's slice. The separation is enforced by
one `RowFilterPolicy`, not by separate databases.

## What this demonstrates

- **Template-driven row filters.** `{{ subject.tenantId }}` binds to a
  positional parameter at rewrite time, so the *same* policy rewrites
  every caller's query against their own tenant value.
- **Default-deny still applies.** A third tenant (`gamma`) has rows in
  the table but no API key in `policies.d/` — no caller can reach that
  data.
- **Audit carries the subject.** Each record names the caller and the
  (public, template-rendered) tenantId, so "who read what" is a single
  `jq` away.

## Layout

```
multi-tenant/
├── README.md                — this file
├── docker-compose.yaml      — single-service Sluice stack
├── server.yaml              — ServerConfig
├── seed.sql                 — 3 tenants × 5 customers × 5 orders
├── policies.d/
│   ├── datasource-shop.yaml  — sqlite @ /data/shop.db
│   ├── bindings-tenants.yaml — API keys `acme` and `beta`
│   ├── allow-analytics.yaml  — SqlAccessPolicy (customers + orders)
│   └── filter-tenant.yaml    — RowFilterPolicy ({{ subject.tenantId }})
└── data/                    — shop.db + audit log (populated at runtime)
```

## Run it

```bash
sqlite3 data/shop.db < seed.sql
docker compose up --build
```

## Query as each tenant

```bash
# acme → sees only rows 1,2 (customers) and 10,11 (orders)
curl -s -H "X-Api-Key: sl_demo_acme.supersecret" \
     -H "Content-Type: application/json" \
     -d '{"sql":"SELECT id, tenant_id, email FROM shop.main.customers ORDER BY id"}' \
     http://localhost:8080/v1/query | jq '.rows'

# beta → sees only rows 3,4 (customers) and 12,13 (orders)
curl -s -H "X-Api-Key: sl_demo_beta.supersecret" \
     -H "Content-Type: application/json" \
     -d '{"sql":"SELECT id, tenant_id, email FROM shop.main.customers ORDER BY id"}' \
     http://localhost:8080/v1/query | jq '.rows'
```

Neither key can reach gamma's rows: no SubjectBinding exists, so the
gamma tenant has no way to authenticate in the first place.

## Why it's safe

The rewriter wraps every FROM-reference to the matched tables in a
subquery with `WHERE tenant_id = $1` and binds `$1` to the rendered
template value. The underlying SQL engine never sees a literal tenant
string — parameter binding is handled by DuckDB, so injection via the
tenantId claim is impossible. You can watch the rewrite by setting
`SLUICE_LOG_LEVEL=debug` and reading the `policy.rewritten` log line.

## Not for production

The API-key hashes in `docker-compose.yaml` are example literals. For a
real deployment, use `./bin/sluice apikey hash --pepper $(vault read
-field=pepper secret/sluice) --material <key-material>` (v0.2) and
store the results in a secret manager.
