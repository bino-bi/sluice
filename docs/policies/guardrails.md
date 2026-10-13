<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Rejects and rewrites

A `QueryRejectPolicy` rejects a query because of its form. A `QueryRewritePolicy` limits the rows and the run time of a query. Sluice applies both after [access control](access-control.md).

## Example

### Reject a query

Goal: agents must name the columns they read.

```yaml
--8<-- "examples/guardrails/policies.d/reject-agent-shapes.yaml:2"
```

```bash
--8<-- "examples/guardrails/steps/01-select-star.sh"
```

```json
--8<-- "examples/guardrails/expected/01-select-star.txt"
```

**What happened:** the rule `no-select-star` was true. Sluice rejected the query with HTTP 403 and the rule's `message`.

### Limit a query

Goal: an agent gets at most three rows and five seconds.

```yaml
--8<-- "examples/guardrails/policies.d/rewrite-agent-limits.yaml:2"
```

```bash
--8<-- "examples/guardrails/steps/02-limit.sh"
```

```json
--8<-- "examples/guardrails/expected/02-limit.txt"
```

```yaml
--8<-- "examples/guardrails/tests/guardrails.yaml:limit"
```

**What happened:** Sluice added `LIMIT 3` to the SQL. The result reached the limit, so the response says `truncated: true`.

## Fields

`spec.reject.rules` needs at least one entry.

| Field | Type | Default | Meaning |
|---|---|---|---|
| `name` | string | | Optional. A [suite](testing.md) names a rule as `policy/rule`. |
| `expression` | string | | [CEL](conditions.md) that returns a boolean. When empty, the rule fires for every matched query. An error at run time denies the query with `ACL_DENIED`. |
| `message` | string | | Text for the client. |
| `code` | string | `ACL_REJECTED` | Error code for the client. The HTTP status stays 403. |

An expression reads `subject.*`, as in [conditions](conditions.md), and these values. Some are partial checks:

| Value | Type | Notes |
|---|---|---|
| `query.has_select_star`, `query.has_where`, `query.has_order_by` | boolean | |
| `query.has_cte`, `query.is_recursive_cte`, `query.has_union`, `query.has_limit` | boolean | `has_union` is also true for `INTERSECT` and `EXCEPT`. |
| `query.is_aggregate` | boolean | True for `GROUP BY`, `HAVING` and these functions: `count`, `sum`, `avg`, `min`, `max`, `bit_and`, `bit_or`, `bit_xor`, `string_agg`, `array_agg`, `json_agg`, `jsonb_agg`. False for other functions, such as `median(x)`, and inside a call, a cast or `CASE`: `round(avg(x))`, `sum(x)::INT`. Any function with `DISTINCT` counts as an aggregate. |
| `query.limit` | int | 0 when the `LIMIT` is not a plain number, such as `500 + 1` or `'500'`. |
| `query.joins` | int | Counts the `JOIN` keyword. A comma join counts as 0. |
| `query.where_columns`, `query.group_by_columns` | list of text | Names as written: `o.status` is not `status`. `where_columns` lists a column that is an operand of an operator, `AND`, `OR`, `NOT` or `IS NULL` of the outer `WHERE`. Operators are comparisons, arithmetic, text joining and `NULLIF`. A column in any other form is missing: a function call, a cast, `CASE`, `IS TRUE`, a value list, a row or a subquery. A bound of `BETWEEN` and a column before `IN (SELECT ...)` are missing too. `group_by_columns` lists only plain column names, not `ROLLUP (x)` or a position such as `1`. |
| `query.catalogs` | list of text | |
| `query.tables` | list of text | Lower case, `catalog.schema.table`. |

`spec.rewrite` needs at least one of these fields. `limit` and `sample` change `SELECT` statements only.

| Field | Type | Default | Meaning |
|---|---|---|---|
| `limit.max` | int | | Row limit, 1 to 2147483647. Sluice adds a `LIMIT` or lowers a larger one. A smaller whole number stays. Any other `LIMIT`, such as `LIMIT 1 + 1`, becomes `max`. |
| `sample.rate` | number | | Share of rows to return, above 0 and at most 1. |
| `sample.method` | string | `reservoir` | `reservoir`, `bernoulli` or `system`. |
| `timeout` | duration | | Longest run time, `"10s"` or seconds as a number. Then the client gets `ERR_TIMEOUT`. |

A policy `timeout` can only shorten the timeout of the request and the server. When several rewrite policies match, Sluice uses the lowest limit, the shortest timeout and the first sample in priority order. A `QueryRewritePolicy` with an empty `spec.match` loads and matches no query.

## More examples

### Sample

```yaml
--8<-- "examples/guardrails/policies.d/rewrite-explorer-sample.yaml:rewrite"
```

```yaml
--8<-- "examples/guardrails/tests/guardrails.yaml:sample"
```

With a limit and a sample, Sluice samples the limited rows.

## Limits

- The client error and the audit record name the policy, not the rule.
- In a `UNION`, the `query.*` values describe only the first SELECT. They do not see a `WITH`, `ORDER BY` or `LIMIT` of the whole statement. They do not see subqueries. Only `query.tables` and `query.catalogs` cover the whole statement.
- Sluice refuses every statement with a `$n` parameter. See [Request lifecycle](../architecture/request-lifecycle.md).
- `request.*` values are always empty.
- A limit or a sample makes Sluice refuse [some SQL forms](row-filters.md#sql-forms-refused-on-filtered-or-masked-tables).

## See also

- [CEL conditions](conditions.md)
- [Testing policies](testing.md)
- [MCP agents](../cookbook/mcp-agents.md)
