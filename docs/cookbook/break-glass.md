<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Break-glass access

**Goal:** an on-call SRE sees the raw `email` column during an incident while
every other analytics user sees it masked — no policy reload, no separate
data path, and every elevated query lands in the hash-chained audit log.
Runnable version: `examples/break-glass/`.

## Ingredients

- One shared `SqlAccessPolicy` — break-glass means *same data, fewer
  safeguards*, not different data.
- One `ColumnMaskPolicy` whose `exclude` selector carves out the `sre` group.
- Two `SubjectBinding` objects: the SRE key carries `groups:
  ["analytics", "sre"]`, so it inherits analyst access and the exclusion.

## The policies

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata:
  name: mask-email
  priority: 50
spec:
  match:
    any:
      - subjects:
          groups: ["analytics"]
        resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["customers"]
          columns: ["email"]
  exclude:
    any:
      - subjects:
          groups: ["sre"]
  mask:
    type: "null"
---
apiVersion: sluice.bino.bi/v1alpha1
kind: SubjectBinding
metadata:
  name: analyst
spec:
  claims:
    subjectId: "day-to-day-analyst"
  apiKeys:
    - id: "analyst"
      hashRef: "secret://env/SLUICE_APIKEY_ANALYST_HASH"
      groups: ["analytics"]
---
apiVersion: sluice.bino.bi/v1alpha1
kind: SubjectBinding
metadata:
  name: sre-on-call
spec:
  claims:
    subjectId: "on-call-sre"
  apiKeys:
    - id: "sre"
      hashRef: "secret://env/SLUICE_APIKEY_SRE_HASH"
      groups: ["analytics", "sre"]
```

The `SqlAccessPolicy` is the same `allow-analytics` policy as in the
[PII-masking recipe](pii-masking.md) — the SRE key matches it through its
`analytics` group membership. The `sre` group on its own grants nothing:
without `analytics`, default-deny still applies.

## Run and verify

```bash
# analyst → email is null
curl -s -H "X-Api-Key: analyst.supersecret" -H "Content-Type: application/json" \
     -d '{"sql":"SELECT id, email FROM shop.main.customers ORDER BY id"}' \
     http://localhost:8080/v1/query | jq -c '.rows'
# [[1,null],[2,null],[3,null]]

# SRE → email in the clear (break-glass in effect)
curl -s -H "X-Api-Key: sre.rotatedtoken" -H "Content-Type: application/json" \
     -d '{"sql":"SELECT id, email FROM shop.main.customers ORDER BY id"}' \
     http://localhost:8080/v1/query | jq -c '.rows'
# [[1,"alice@acme.example"],[2,"bob@acme.example"],[3,"carol@acme.example"]]
```

As a policy test: the analyst case expects
`masks: ["shop.main.customers.email=null"]`, the SRE case expects `masks: []`.

## Audit follow-up

Both queries — masked and elevated — are appended to the same hash-chained
audit log with the caller's subject and groups:

```bash
# live view via the admin API
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
     "http://localhost:9091/admin/audit/tail?n=20" | jq .

# verify the chain, then extract every elevated query
sluice audit verify data/audit          # exit code 4 = chain broken
jq 'select(.subject.groups | index("sre"))' data/audit/audit-*.jsonl
```

Feed the audit directory into your SIEM and alert on any record whose
`subject.groups` contains `sre` — break-glass that nobody reviews is just a
backdoor.

## Pitfalls

- **Exclusion scope.** For per-table kinds (`ColumnMaskPolicy`,
  `RowFilterPolicy`) the `exclude` selector is applied per table. The exclude
  above names only a subject, so it lifts the mask on *every* table the policy
  matches; add `resources:` to the exclude clause to lift it only for specific
  tables. For whole-query kinds (`SqlAccessPolicy`, `QueryRejectPolicy`) a
  matching exclude drops the policy for the entire query.
- **Standing power.** This exclusion is always on: anyone holding the `sre`
  group bypasses the mask at 3 a.m. on a quiet Tuesday, too. The audit log
  records it, but nobody consents beforehand. When PII exposure needs a
  human decision per query, use an
  [ApprovalPolicy](approval-workflow.md) instead — time-boxed, single-use,
  and explicitly accepted by a second person.

## See also

- [Column masks](../policies/column-masks.md) — mask providers and conflict resolution.
- [Matching & precedence](../policies/matching.md) — selector and exclude semantics.
- [Audit trail](../security/audit.md) — chain verification and record schema.
