<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# hello-sluice

The shortest end-to-end demonstration of Sluice: a SQLite catalog, one allow policy, one row filter, one column mask, an API-key identity binding, and a `curl` round-trip through `POST /v1/query`.

By the end of this example you will have:

- A Sluice server running on `:8080` against a seeded SQLite database.
- A caller identified by an API key tied to a specific tenant.
- A query that is rewritten on the wire to (a) filter to the caller's tenant and (b) null out the `email` column.
- An audit entry on disk, verifiable with `sluice audit verify`.

## What's in this directory

```
hello-sluice/
├── README.md                    — this file
├── docker-compose.yaml          — one-service stack: Sluice + bind mounts
├── server.yaml                  — ServerConfig
├── seed.sql                     — schema + sample rows for the SQLite catalog
├── policies.d/                  — all policy objects live in one directory
│   ├── datasource-shop.yaml     — DataSource pointing at /data/shop.db
│   ├── binding-apikey.yaml      — SubjectBinding with one API key
│   ├── allow-analytics.yaml     — SqlAccessPolicy
│   ├── filter-tenant.yaml       — RowFilterPolicy (tenant_id = {{ subject.tenantId }})
│   └── mask-email.yaml          — ColumnMaskPolicy (null)
└── data/                        — populated at first run with shop.db
```

> Sluice loads every `apitypes.Object` kind — `DataSource`, `SubjectBinding`,
> and each of the five policy kinds — from the single `policies.directory`
> configured in `server.yaml`. There's no separate datasources directory.

## 1. Seed the SQLite database

From this directory:

```bash
sqlite3 data/shop.db < seed.sql
```

This creates two tables under the `main` schema — `customers` and `orders` — with three tenants worth of sample rows.

## 2. Start Sluice

### With Docker

```bash
docker compose up --build
```

The image is built from the repository root. The service listens on `:8080` (REST) and `:9091` (admin, optional).

### Without Docker

```bash
# From the repository root:
make build
./bin/sluice serve \
  --config examples/hello-sluice/server.yaml \
  --policies-dir examples/hello-sluice/policies.d
```

You should see a log line containing `"event":"server started","transport":"rest"`.

## 3. Issue a query

The example API key is `sl_demo_hello.world` — key id `sl_demo_hello`, material `world`. The identifier splits on the last `.` and treats everything to the left as the id. Its HMAC is pre-computed with pepper `hello-sluice-demo-pepper` and stored in the `SLUICE_APIKEY_HELLO_HASH` env var referenced by `policies.d/binding-apikey.yaml`.

```bash
curl -s \
  -H "X-Api-Key: sl_demo_hello.world" \
  -H "Content-Type: application/json" \
  -d '{"sql":"SELECT id, email, tenant_id FROM shop.main.customers ORDER BY id"}' \
  http://localhost:8080/v1/query | jq .
```

Expected response (trimmed):

```json
{
  "query_id": "01HQ...",
  "columns": ["id", "email", "tenant_id"],
  "rows": [
    [1, null, "acme"],
    [2, null, "acme"]
  ],
  "row_count": 2,
  "truncated": false
}
```

Three things to notice:

1. **Row filter applied.** Although `customers` has rows for three tenants, only the two rows with `tenant_id = 'acme'` are returned — the row-filter policy rewrote the statement to `WHERE tenant_id = $1` and bound `$1 = 'acme'`.
2. **Column mask applied.** The `email` column is `null` — the mask policy substituted `NULL AS email` in the target list.
3. **query_id in the response.** This is the ULID of the audit record for this request.

## 4. Verify the audit trail

```bash
ls data/audit/
# audit-2026-04-20.jsonl

./bin/sluice audit verify data/audit
# chain OK (1 file(s), 2 record(s), last_hash=...)
```

The `2 records` count is the genesis record (written on startup) plus the one query.

## 5. Try a denied query

```bash
curl -s \
  -H "X-Api-Key: sl_demo_hello.world" \
  -H "Content-Type: application/json" \
  -d '{"sql":"SELECT * FROM shop.main.admin_users"}' \
  http://localhost:8080/v1/query | jq .
```

The `allow-analytics` policy only grants access to `shop.main.customers` and `shop.main.orders`. Anything else falls through to the default-deny and returns:

```json
{
  "code": "ACL_DENIED",
  "message": "no SqlAccessPolicy matched (default-deny)",
  "query_id": "01HQ..."
}
```

The `query_id` in the error response appears in the audit log as a `deny` record — same hash-chain, same verifiability.

## 6. Tear down

```bash
docker compose down -v
# or just ^C if you ran `sluice serve` directly
```

## Where to go from here

- `examples/multi-tenant/` — multiple `SubjectBinding` issuers with tenant isolation. _(ships with v0.2.)_
- `examples/pii-masking/` — `partial` and `hash` providers. _(ships with v0.2.)_
- `examples/cross-source-join/` — Postgres + S3 Parquet in a single query. _(ships with v0.2.)_
- `docs/reference/policy-schema.md` — the full policy DSL. _(ships with v0.2.)_
