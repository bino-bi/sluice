<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# break-glass

An on-call SRE needs to see the raw `email` column during an incident.
Every other analytics user sees it masked. No policy reload is required
to switch modes — the `sre` group claim on the API key does the work,
and every elevated query is written to the hash-chained audit log with
the elevated group plainly recorded.

## What this demonstrates

- **Selector exclusions on column masks.** The mask policy matches
  `subjects.groups=[analytics]` but excludes `subjects.groups=[sre]`.
  The SRE key belongs to *both* groups — the exclude wins.
- **Audit as the accountability ledger.** There is no separate
  "break-glass" log. The standard audit record captures the subject,
  their groups, the SQL, and the decision. `sluice audit verify`
  covers the same chain whether the caller was elevated or not.
- **Least-privilege by default.** The SRE key inherits the analytics
  access policy; it does not get a separate `SqlAccessPolicy` because
  the whole point of break-glass is "same data, fewer safeguards", not
  "different data".

## Layout

```
break-glass/
├── README.md
├── docker-compose.yaml
├── server.yaml
├── seed.sql
├── policies.d/
│   ├── datasource-shop.yaml
│   ├── bindings.yaml           — two API keys: `analyst` and `sre`
│   ├── allow-analytics.yaml    — SqlAccessPolicy (shared)
│   └── mask-email.yaml         — ColumnMaskPolicy + exclude: sre
└── data/
```

## Run it

```bash
sqlite3 data/shop.db < seed.sql
docker compose up --build
```

## Observe the difference

```bash
# analyst → email is null
curl -s -H "X-Api-Key: sl_demo_analyst.supersecret" \
     -H "Content-Type: application/json" \
     -d '{"sql":"SELECT id, email FROM shop.main.customers ORDER BY id"}' \
     http://localhost:8080/v1/query | jq '.rows'
# [[1,null],[2,null],[3,null]]

# SRE → email visible (break-glass in effect)
curl -s -H "X-Api-Key: sl_demo_sre.rotatedtoken" \
     -H "Content-Type: application/json" \
     -d '{"sql":"SELECT id, email FROM shop.main.customers ORDER BY id"}' \
     http://localhost:8080/v1/query | jq '.rows'
# [[1,"alice@acme.example"],[2,"bob@acme.example"],[3,"carol@acme.example"]]
```

## Review the elevated queries

```bash
./bin/sluice audit verify data/audit
# chain OK (1 file(s), 3 record(s), last_hash=...)

# Grep for elevated subjects:
jq 'select(.subject.groups | index("sre"))' data/audit/audit-*.jsonl
```

Every record written while the SRE key was in use carries
`subject.groups: ["analytics","sre"]`. Because the chain is
tamper-evident (SHA-256 over canonical JSON), removing or editing
these records invalidates the chain and trips the next
`sluice audit verify` run.

## Operational discipline

Break-glass is a mechanism, not an excuse. Treat every SRE query as a
reviewable event:

1. Rotate the SRE API key on a calendar cadence (quarterly or tighter).
2. Feed `data/audit/` into your SIEM so a separate team sees every
   elevated record — SREs cannot review their own break-glass usage.
3. Cover `subject.groups` includes `"sre"` with a paging alert whose
   threshold is "any non-zero count over a business day", so the
   incident team finds out the same day an elevation happened.

## Not for production

Real break-glass deployments pair this mechanism with
just-in-time group issuance (e.g. a short-lived OIDC token from your
IdP) so the SRE key itself doesn't carry the elevated group at rest.
The example uses long-lived API keys for readability; v0.3 will ship
the OIDC bindings needed to reproduce the flow with JWTs.
