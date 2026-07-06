<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# pii-masking

Four PII columns — `email`, `phone`, `ssn`, `birth_date` — and one
analyst who needs to work on the table without seeing the plaintext
values. Three of the four columns are each neutralised by a
`ColumnMaskPolicy` to show the two simplest providers (`null`,
`constant`) in action; `birth_date` is deliberately left unmasked.

> **More providers exist.** Beyond `null` and `constant`, Sluice ships
> `partial` (keep the first/last characters), `hash` (SHA-256 in-query,
> or HMAC-SHA256 with a `secret://` salt applied post-query), `fpe`
> (format-preserving encryption), `fake` (deterministic fake values),
> and more. This example sticks to the two simplest; see
> `docs/policies/column-masks.md` for the full catalog.

## What this demonstrates

- **Per-column granularity.** Three masks, three columns, two
  providers — priorities only matter when two masks collide on the
  same column.
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

The rewritten SQL is never logged. To inspect it, write a policy test
with a `rewrittenSqlContains` expectation and run
`./bin/sluice policy test policies.d`, or use
`./bin/sluice policy explain` to see which policies fire for a given
subject and table. For the query above, the effective SQL is roughly:

```sql
SELECT id,
       NULL              AS email,
       '+1-555-REDACTED' AS phone,
       'REDACTED'        AS ssn,
       birth_date
FROM   shop.main.customers
ORDER  BY id
```

The substitution is not limited to the target list: masked column
references are replaced everywhere in the statement — SELECT list,
`WHERE`/`HAVING`, `GROUP BY`/`ORDER BY`, and `JOIN` conditions. A
predicate like `WHERE email LIKE '%acme%'` is rewritten to compare
against `NULL` and returns no rows, so a masked column cannot be used
to filter on plaintext values either.

## Not for production

The constant masks here are placeholders suitable for a demo. For real
PII workloads use a `hash` mask with `algorithm: hmac_sha256` and a
salt supplied via a `secret://env/...` or `secret://file/...` reference
(see `docs/policies/column-masks.md`), rotated on a calendar cadence.
