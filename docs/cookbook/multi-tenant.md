<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Multi-tenant isolation

**Goal:** three tenants share one `customers` + `orders` catalog; each API key
sees only its tenant's rows — enforced by a single `RowFilterPolicy`, not by
separate databases. Runnable version: `examples/multi-tenant/`.

## Ingredients

- One `SqlAccessPolicy` allowing the `analytics` group to read both tables.
- One `RowFilterPolicy` templating `tenant_id` from the caller's claims.
- One `SubjectBinding` per tenant, stamping a literal `tenantId` claim onto
  that tenant's API key.
- A `sqlite` DataSource (`shop`) seeded with three tenants' rows.

## The policies

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata: { name: allow-analytics, priority: 100 }
spec:
  effect: allow
  match:
    any:
      - subjects:
          groups: ["analytics"]
        resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["customers", "orders"]
---
apiVersion: sluice.bino.bi/v1alpha1
kind: RowFilterPolicy
metadata: { name: filter-tenant, priority: 80 }
spec:
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["customers", "orders"]
  combine: restrictive
  filter:
    predicate:
      column: tenant_id
      op: Equals
      value: "{{ subject.tenantId }}"
---
apiVersion: sluice.bino.bi/v1alpha1
kind: SubjectBinding
metadata: { name: tenant-acme }
spec:
  claims: { subjectId: "acme-analyst", tenantId: "acme" }
  apiKeys:
    - { id: "acme", hashRef: "secret://env/SLUICE_APIKEY_ACME_HASH", groups: ["analytics"] }
---
apiVersion: sluice.bino.bi/v1alpha1
kind: SubjectBinding
metadata: { name: tenant-beta }
spec:
  claims: { subjectId: "beta-analyst", tenantId: "beta" }
  apiKeys:
    - { id: "beta", hashRef: "secret://env/SLUICE_APIKEY_BETA_HASH", groups: ["analytics"] }
```

One binding per tenant: in API-key mode the binding-level `claims` values are
stamped as literal claims on the authenticated session, so
`{{ subject.tenantId }}` resolves to `acme` for the acme key and `beta` for
the beta key. The template renders as a bound `$1` parameter — the tenant
value is never spliced into the SQL string.

## Run and verify

From `examples/multi-tenant/`: seed the database, generate real key hashes
(the values checked into `docker-compose.yaml` are placeholders — replace
them), and start the stack.

```bash
sqlite3 data/shop.db < seed.sql
sluice apikey hash --pepper multi-tenant-demo-pepper --id acme --material supersecret
sluice apikey hash --pepper multi-tenant-demo-pepper --id beta --material supersecret
# paste the two hashes into the environment: block of docker-compose.yaml
docker compose up --build
```

Run the **same** query as both tenants:

```bash
curl -s -H "X-Api-Key: acme.supersecret" -H "Content-Type: application/json" \
     -d '{"sql":"SELECT id, tenant_id, email FROM shop.main.customers ORDER BY id"}' \
     http://localhost:8080/v1/query | jq -c '.rows'
# [[1,"acme","a1@acme.example"],[2,"acme","a2@acme.example"]]
```

Repeat with `X-Api-Key: beta.supersecret` — the result sets are disjoint
(`beta` sees rows 3 and 4 only). The rewritten SQL is:

```sql
SELECT id, tenant_id, email FROM (SELECT * FROM shop.main.customers WHERE tenant_id = $1) customers ORDER BY id
```

The third tenant, `gamma`, has rows in the table but no `SubjectBinding` — no
credential exists that could reach them, and default-deny blocks everyone else.

## Test it without a server

Save as `policies.d/tests/isolation.yaml` and run `sluice policy test policies.d`:

```yaml
cases:
  - name: acme analyst gets the tenant filter on customers
    identity: { subject: acme-analyst, groups: [analytics], claims: { tenantId: acme } }
    sql: "SELECT id, tenant_id, email FROM shop.main.customers"
    expect:
      outcome: allow
      filters: ["shop.main.customers"]
      applied: ["RowFilterPolicy/filter-tenant", "SqlAccessPolicy/allow-analytics"]
      rewrittenSqlContains: ["tenant_id = $1"]

  - name: subject without a binding is denied by default
    identity: { subject: stranger, groups: [guests] }
    sql: "SELECT id FROM shop.main.customers"
    expect:
      outcome: deny
```

## Common error

!!! danger "Do not copy this shape — it never worked"
    Old drafts and third-party posts sometimes show a dialect that Sluice has
    never accepted. If you see any of the following, the file will not load:

    ```yaml
    # BROKEN — wrong apiVersion, predicate directly under spec, lowercase op
    apiVersion: sluice.bino-bi.github.io/v1
    kind: RowFilterPolicy
    metadata: { name: filter-tenant }
    spec:
      selector: { groups: ['analytics'] }
      tables: ['orders', 'customers']
      predicate:
        column: tenant_id
        op: equals
        value: "{{ subject.tenantId }}"
    ```

    The only accepted form is `apiVersion: sluice.bino.bi/v1alpha1` with
    `spec.match.any[]` selectors and the predicate nested under
    `spec.filter.predicate` with a PascalCase `op:` (`Equals`), as shown above.
    `sluice policy validate policies.d` rejects the block immediately with
    `unknown kind sluice.bino-bi.github.io/v1/RowFilterPolicy`; fixing only
    the apiVersion surfaces the next error,
    `spec.match: either any or all must be non-empty` (because `selector:`
    is not `match:`).

## See also

- [Row filters](../policies/row-filters.md) — predicate tree, CEL expressions, `combine`.
- [Subjects, keys & budgets](../policies/subjects.md) — bindings and API-key hashing.
- [Testing policies](../policies/testing.md) — the full test-suite schema.
