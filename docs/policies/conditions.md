<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Conditions

`spec.conditions` holds [CEL](../reference/glossary.md) expressions. A policy applies only when its [selector](matching.md) matches and every condition is true.

## Example

Only subjects with the claim `tier: gold` read `reports`.

```yaml
--8<-- "examples/conditions/policies.d/allow-reports-gold.yaml:2"
```

The [suite](testing.md):

```yaml
--8<-- "examples/conditions/tests/claim.yaml:2"
```

```bash
--8<-- "examples/conditions/steps/01-test-claim.sh"
```

```text
--8<-- "examples/conditions/expected/01-test-claim.txt"
```

**What happened:** only the first subject passes the condition. No policy allows the other two subjects, so Sluice denies them.

## Fields

| Field | Type | Default | Meaning |
|---|---|---|---|
| `conditions[].name` | string | none | Shown in error messages. |
| `conditions[].expression` | string | required | CEL expression that returns a boolean. |

Sluice checks every expression at load. A value alone, such as `query.has_cte`, is a load error. Write `query.has_cte == true`.

### Variables

| Variable | Type | Value |
|---|---|---|
| `subject.id`, `subject.email`, `subject.issuer` | string | Id, email and issuer of the subject. |
| `subject.groups` | list | Groups of the subject. |
| `subject.claims` | map | Claims of the subject. |
| `subject.auth_method` | string | `jwt` or `api_key`. |
| `request.remote_ip`, `request.user_agent` | string | IP address and user agent of the client. Empty over stdio. |
| `request.headers` | map | Request headers, names in lower case: `request.headers["x-region"]`. Credential headers are left out. |
| `query.tables` | list | Tables as `catalog.schema.table`, lower case. A missing part stays empty: `..orders`. |
| `query.catalogs` | list | Catalogs that the query reads. |
| `query.joins` | int | Count of `JOIN` keywords and of commas between `FROM` items. |
| `query.has_limit`, `query.limit` | boolean, int | The query has a `LIMIT`, and its value, or 0 when that is not a plain number, such as `500 + 1` or `'500'`. |
| `query.has_where`, `query.where_columns` | boolean, list | The query has a `WHERE`, and its columns. |
| `query.is_aggregate`, `query.group_by_columns` | boolean, list | The query aggregates, and its `GROUP BY` columns. |
| `query.has_select_star`, `query.has_order_by`, `query.has_union` | boolean | The query has `*`, `ORDER BY`, or a `UNION`, `INTERSECT` or `EXCEPT`. |
| `query.has_cte`, `query.is_recursive_cte` | boolean | The query has `WITH` or `WITH RECURSIVE`. |

The rules of a [`QueryRejectPolicy`](guardrails.md) use the same variables. That page says more about the `query` values.

## More examples

### Check the query

This allow needs a `LIMIT` and a `query.limit` of at most 100.

```yaml
--8<-- "examples/conditions/policies.d/allow-orders-small.yaml:2"
```

```yaml
--8<-- "examples/conditions/tests/conditions.yaml:limit"
```

A `LIMIT` that is not a plain number still passes, so this is not a row limit. For that, use a [`QueryRewritePolicy`](guardrails.md).

### A condition that fails

A lookup of a missing claim is an error, not `false`. Sluice then denies the whole query with `ACL_DENIED`, also when another policy allows the table.

```yaml
--8<-- "examples/conditions/policies.d/mask-card-number.yaml:2"
```

```yaml
--8<-- "examples/conditions/tests/conditions.yaml:error"
```

`sluice policy explain` shows the reason:

```bash
--8<-- "examples/conditions/steps/02-explain-error.sh"
```

```text
--8<-- "examples/conditions/expected/02-explain-error.txt"
```

## Limits

- No variable holds the current time.
- Most `query.*` values do not read subqueries. See [Rejects and rewrites](guardrails.md).
- A row filter has its own CEL form. See [Row filters](row-filters.md).

## See also

- [Matching](matching.md)
- [Rejects and rewrites](guardrails.md)
- [Testing policies](testing.md)
