<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Multi-tenant isolation

Goal: every analyst sees only their tenant's rows, without the SQL
they write caring about tenant IDs.

## Ingredients

- One `SqlAccessPolicy` granting SELECT on the shared tables.
- One `RowFilterPolicy` templating `tenant_id` from the subject.
- One `SubjectBinding` lifting the tenant claim from the JWT.

## Recipe

```yaml
---
apiVersion: sluice.bino-bi.github.io/v1
kind: SubjectBinding
metadata: { name: idp-tenants }
spec:
  issuer: https://auth.example.com/
  audience: sluice
  jwksUrl: https://auth.example.com/.well-known/jwks.json
  claims:
    subject: '$.sub'
    email:   '$.email'
    groups:  '$.realm_access.roles'
    tenantId: '$.custom.tenant_id'
---
apiVersion: sluice.bino-bi.github.io/v1
kind: SqlAccessPolicy
metadata: { name: analytics-select }
spec:
  priority: 100
  effect:   allow
  selector: { groups: ['analytics'] }
  tables:   ['orders', 'customers', 'products']
  statements: ['SELECT']
---
apiVersion: sluice.bino-bi.github.io/v1
kind: RowFilterPolicy
metadata: { name: tenant-isolation }
spec:
  priority: 90
  mode: restrictive
  selector: { groups: ['analytics'] }
  tables:  ['orders', 'customers']
  expression: 'tenant_id = {{ subject.tenantId }}'
```

## Notes

- `tenant_id` never appears in the analyst's SQL. The rewriter wraps
  every matching FROM table with `(SELECT * FROM <t> WHERE tenant_id =
  $1)`.
- Cross-tenant joins within a single policy scope are automatically
  intersected — two tables with the filter both get the wrap.
- Verify with `sluice policy explain --user alice --table public.orders`.
