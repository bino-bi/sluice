<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Cross-source joins

Goal: a single query joins `pg.public.orders` (Postgres) against
`s3.events` (S3 Parquet) — the policy decisions apply to both sides.

## Why this works

DuckDB is the execution engine; every `DataSource` is attached into a
single query plane. The rewriter walks the full AST regardless of
which catalog a `FROM` table comes from, so row filters and column
masks on either side compose as you'd expect.

## Example query

```sql
SELECT o.id, o.amount, e.user_agent
FROM   pg.public.orders o
JOIN   s3.events e ON e.order_id = o.id
WHERE  o.region = 'eu'
```

With a row filter on `orders` and a column mask on `events.ip_address`,
the rewrite output is:

```sql
SELECT o.id, o.amount, e.user_agent
FROM   ( SELECT * FROM pg.public.orders WHERE tenant_id = $1 ) o
JOIN   ( SELECT id, user_agent, NULL AS ip_address FROM s3.events ) e
  ON   e.order_id = o.id
WHERE  o.region = 'eu'
```

Both transformations happen in one pass; neither catalog needs to know
the other exists.

## Guardrails

The `no-cross-catalog-join` reject rule is available but **off by
default** in MVP — concept §9 resolution #7 keeps cross-source joins
permitted because that is a core Sluice value prop. If your compliance
posture forbids them, add:

```yaml
apiVersion: sluice.bino-bi.github.io/v1
kind: QueryRejectPolicy
metadata: { name: no-cross-catalog }
spec:
  rules: [ { rule: 'no-cross-catalog-join' } ]
```
