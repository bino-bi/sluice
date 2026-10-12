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

`spec.reject.rules` needs at least one entry. All fields are text.

| Field | Default | Meaning |
|---|---|---|
| `name` | | Optional name. |
| `expression` | | CEL that returns a boolean. When empty, the rule fires for every matched query. An error at run time denies the query with `ACL_DENIED`. |
| `message` | | Text for the client. |
| `code` | `ACL_REJECTED` | Error code for the client. The HTTP status stays 403. |

An expression reads `subject.*`, as in [conditions](conditions.md), and these values:

| Value | Type |
|---|---|
| `query.has_select_star`, `query.is_aggregate`, `query.has_where`, `query.has_order_by` | boolean |
| `query.has_cte`, `query.is_recursive_cte`, `query.has_union`, `query.has_limit` | boolean |
| `query.limit` | int. 0 when the `LIMIT` is not a constant |
| `query.joins` | int |
| `query.where_columns`, `query.group_by_columns`, `query.catalogs` | list of text |
| `query.tables` | list of text, lower case, `catalog.schema.table` |

`spec.rewrite` needs at least one of these fields. `limit` and `sample` change `SELECT` statements only.

| Field | Type | Default | Meaning |
|---|---|---|---|
| `limit.max` | int | | Row limit, 1 to 2147483647. Sluice adds a `LIMIT` or lowers a larger one. A smaller `LIMIT` stays. |
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
- Except for `tables` and `catalogs`, the `query.*` values ignore subqueries and the second part of a `UNION`.
- Sluice refuses a `LIMIT $1` parameter with `ERR_UNSUPPORTED_SYNTAX`.
- `request.*` values are always empty.
- A limit or a sample makes Sluice refuse [some SQL forms](row-filters.md#sql-forms-refused-on-filtered-or-masked-tables).

## See also

- [CEL conditions](conditions.md)
- [Testing policies](testing.md)
- [MCP agents](../cookbook/mcp-agents.md)
