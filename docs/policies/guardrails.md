<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Query guardrails

Two kinds shape *queries* rather than data: `QueryRejectPolicy` vetoes statements whose shape you
never want to run, and `QueryRewritePolicy` bounds the cost of the ones that do run. Both only act
on requests that passed [access control](access-control.md) — precedence is deny > reject >
approval.

## Rejecting queries by shape

`spec.reject.rules[]` is a list of named vetoes:

| Field | Meaning |
| ----- | ------- |
| `name` | recommended; identifies the rule in the error and the audit record (not enforced by validation — an unnamed rule still loads) |
| `expression` | CEL over `query.*`, `subject.*`, `request.*`; **empty = fires whenever the selector matches** |
| `message` | client-visible explanation |
| `code` | error code returned to the client, default `ACL_REJECTED` |

The `query` variable exposes the parsed statement's shape:

| Variable | Type |
| -------- | ---- |
| `query.has_select_star`, `query.is_aggregate` | bool |
| `query.has_cte`, `query.is_recursive_cte`, `query.has_union` | bool |
| `query.has_limit`, `query.limit` | bool, int |
| `query.has_order_by`, `query.has_where` | bool |
| `query.where_columns`, `query.group_by_columns` | list of strings |
| `query.joins` | int |
| `query.catalogs`, `query.tables` | list of strings (`tables` as `catalog.schema.table`) |

There is deliberately no `now` variable — decisions must be reproducible for the rewrite cache. A
rule whose expression errors at runtime denies the request (fail-closed).

!!! tip "Expressions must type-check as bool"
    `query` is a dynamic map, so a bare field like `query.has_select_star` does not compile on
    its own — compare explicitly: `query.has_select_star == true`. Compound expressions
    (`query.has_select_star && query.joins > 0`) already return bool and need no change.

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: QueryRejectPolicy
metadata: { name: shape-guardrails, priority: 200 }
spec:
  match: { any: [{ resources: { catalogs: ["shop"] } }] }
  reject:
    rules:
      - name: no-star-joins
        expression: "query.has_select_star && query.joins > 0"
        message: "select explicit columns when joining"
      - name: no-recursive-cte
        expression: "query.is_recursive_cte == true"
        message: "recursive CTEs are not allowed through the gateway"
      - name: no-cross-catalog
        expression: "size(query.catalogs) > 1"
        message: "queries must stay within one catalog"
```

A rule with no expression turns the policy into a selector-driven veto — useful when the selector
alone (a group, an IP range, a table pattern) says enough and you want a friendlier message than a
plain deny.

## Bounding queries

`spec.rewrite` must set **at least one** of `limit`, `sample`, or `timeout`.

!!! warning "Hints are rejected"
    The `hint` field parses but is rejected at compile time ("hints are not supported") — nothing
    consumes hints yet, and silently accepting an inert instruction would misrepresent the
    enforced posture. A policy carrying one fails `sluice policy validate`.

### limit

`limit.max` (integer, 1 to 2147483647) is an inject-or-clamp cap: a SELECT without a `LIMIT` gets
one injected; a constant `LIMIT` above the cap is clamped; a constant at or below it is left
alone; a parameter or expression limit is replaced by the cap. Non-SELECT statements (`EXPLAIN`,
`SET`, `SHOW`, `PRAGMA`) are skipped.

```sql
-- client sends (policy: limit.max = 1000)
SELECT id, total FROM shop.main.orders
-- sluice executes
SELECT id, total FROM shop.main.orders LIMIT 1000
```

### sample

`sample.rate` is a float in `(0, 1]`; `sample.method` is `reservoir` (default), `bernoulli`, or
`system` (lowercase). The deparsed SELECT is wrapped in DuckDB's `USING SAMPLE`:

```sql
SELECT * FROM (SELECT id, total FROM shop.main.orders) AS sluice_sample USING SAMPLE 10% (reservoir)
```

Sampling applies to SELECT statements only; anything else skips the wrap. Every token in the
clause is compile-validated — no user-controlled text enters it.

### timeout

`timeout` is a duration (`"10s"`). It is enforced by the query service canceling execution — it
never appears in the SQL. It can only *shorten* the effective deadline: the client's requested
`timeout_ms` and the server-wide `limits.queryTimeout`/`limits.maxQueryTimeout` caps still apply,
and the earliest deadline wins. On expiry the client receives `ERR_TIMEOUT`.

### Folding across policies

When several `QueryRewritePolicy` objects match one query, they fold in the restrictive
direction: the **minimum** `limit.max`, the **minimum** `timeout`, and the **first** `sample`
instruction in policy order (priority descending, then name ascending). You can therefore layer a
tenant-wide ceiling under a stricter per-group cap without coordination.

## Recipes

**The AI-agent guardrail** — anything in the `agents` group gets at most 1000 rows and 10
seconds, no matter what it asks for:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: QueryRewritePolicy
metadata: { name: agent-guardrail, priority: 300 }
spec:
  match: { any: [{ subjects: { groups: ["agents"] } }] }
  rewrite:
    limit: { max: 1000 }
    timeout: "10s"
```

**Keep agents away from unbounded scans** — pair the cap with a shape veto:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: QueryRejectPolicy
metadata: { name: agent-no-star, priority: 300 }
spec:
  match: { any: [{ subjects: { groups: ["agents"] } }] }
  reject:
    rules:
      - name: no-select-star
        expression: "query.has_select_star == true"
        message: "list the columns you need instead of SELECT *"
```

**Exploration catalog runs on samples** — ad-hoc analysis against the lake sees 10% of rows:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: QueryRewritePolicy
metadata: { name: lake-sample, priority: 100 }
spec:
  match: { any: [{ resources: { catalogs: ["lake"] } }] }
  rewrite:
    sample: { rate: 0.1, method: reservoir }
```
