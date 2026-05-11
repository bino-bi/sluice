<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# pii-masking

Four PII columns — `email`, `phone`, `ssn`, `birth_date` — and one
analyst who needs to work on the table without seeing the plaintext
values. Each column is neutralised by a different `ColumnMaskPolicy` to
show the MVP providers (`null`, `constant`) in action.

> **Note on the v0.2 providers.** `partial` (e.g. `"al***@example.com"`)
> and `hash` (e.g. HMAC-SHA256 with a salt pulled from the secret store)
> are declared in the policy schema but not yet wired into the rewriter.
> Until they land in v0.2 the rewriter treats them as `null`. This
> example only uses the two MVP providers.

## What this demonstrates

- **Per-column granularity.** Three masks, three columns, three
  different effects — priorities only matter when two masks collide on
  the same column.
- **`null` vs `constant`.** Choosing between them is a UX decision, not
  a security one. Both preserve the SQL shape so downstream consumers
  don't break; `constant` keeps a non-null value visible so dashboards
  that count-distinct on the column still work.
- **`birth_date` stays intact.** Only the columns that are explicitly
  matched by a `ColumnMaskPolicy` are masked. Don't rely on default-deny
  here — policies mask; they don't hide.

## Layout

```
pii-masking/
├── README.md
├── docker-compose.yaml
├── server.yaml
├── seed.sql                    — 4 customers × 4 PII columns
├── policies.d/
│   ├── datasource-shop.yaml
│   ├── binding-apikey.yaml     — one API key in the `analytics` group
│   ├── allow-analytics.yaml    — SqlAccessPolicy (customers only)
│   ├── mask-email.yaml         — null
│   ├── mask-phone.yaml         — constant "+1-555-REDACTED"
│   └── mask-ssn.yaml           — constant "REDACTED"
└── data/                       — shop.db + audit log (runtime)
```

## Run it

```bash
sqlite3 data/shop.db < seed.sql
docker compose up --build
```

## Query and compare

```bash
curl -s -H "X-Api-Key: sl_demo_analyst.supersecret" \
     -H "Content-Type: application/json" \
     -d '{"sql":"SELECT id, email, phone, ssn, birth_date FROM shop.main.customers ORDER BY id"}' \
     http://localhost:8080/v1/query | jq '.rows'
```

Expected:

```json
[
  [1, null, "+1-555-REDACTED", "REDACTED", "1988-03-14"],
  [2, null, "+1-555-REDACTED", "REDACTED", "1975-07-22"],
  [3, null, "+1-555-REDACTED", "REDACTED", "1990-11-30"],
  [4, null, "+1-555-REDACTED", "REDACTED", "2001-02-02"]
]
```

## Seeing the rewrite

Set `SLUICE_LOG_LEVEL=debug` in the compose file and look for the
`policy.rewritten` log line — the effective SQL is roughly:

```sql
SELECT id,
       NULL              AS email,
       '+1-555-REDACTED' AS phone,
       'REDACTED'        AS ssn,
       birth_date
FROM   shop.main.customers
ORDER  BY id
```

— i.e. the rewriter replaces the column reference in the *target list*
only. Predicates that name `email` still resolve against the underlying
column, so a query like `WHERE email LIKE '%acme%'` keeps working — the
mask affects *projection*, not *filter visibility*. If you need both,
layer a `RowFilterPolicy` on top (see `examples/multi-tenant`).

## Not for production

The constant masks here are placeholders suitable for a demo. For real
PII workloads pair a `hash` mask (coming in v0.2) with a salt stored in
Vault / AWS / GCP and rotated on a calendar cadence.
