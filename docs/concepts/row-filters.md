<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Row filters

A row filter injects a `WHERE` predicate into the query before
execution. The rewriter wraps each matching `FROM` table in a subquery:

```sql
-- incoming
SELECT id, amount FROM orders

-- rewritten (tenant_id taken from subject claim)
SELECT id, amount FROM (
  SELECT * FROM orders WHERE tenant_id = $1
) orders
```

The alias is preserved so outer references (`ORDER BY orders.id`, joins
against `orders.customer_id`) resolve unchanged.

## Templates

`RowFilterPolicy.spec.expression` accepts `{{ subject.* }}` and
`{{ request.* }}` templates. Rendered values always flow through
positional parameters (`$1`, `$2`, …). Sluice never concatenates
untrusted strings into the rewritten SQL — the templating step lowers a
reference to a parameter binding.

```yaml
apiVersion: sluice.bino-bi.github.io/v1
kind: RowFilterPolicy
metadata:
  name: tenant-isolation
spec:
  priority: 100
  mode: restrictive
  selector:
    groups: ['analytics']
  tables: ['orders', 'customers']
  expression: 'tenant_id = {{ subject.tenantId }}'
```

## Compositional semantics

Two policies that both match are combined:

- `restrictive` policies are intersected with `AND`.
- `permissive` policies are unioned with `OR`.
- Mixing both yields `(permissive OR …) AND (restrictive AND …)`.

The order of policies in the directory does not change the outcome;
only their `priority` and `mode` fields do.
