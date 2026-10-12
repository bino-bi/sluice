<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Conditions

`spec.conditions` holds CEL expressions. A policy applies only when its selector matches and every condition is true.

## Example

Interns read `orders` only with a `LIMIT` of 100 rows or less.

```yaml
--8<-- "examples/conditions/policies.d/allow-orders-small.yaml:2"
```

```yaml
--8<-- "examples/conditions/tests/conditions.yaml:limit"
```

The first query passes the condition. The second has no `LIMIT`, so the allow does not apply. No other policy allows the table.

## Fields

| Field | Type | Meaning |
|---|---|---|
| `conditions[].name` | string | Shown in error messages. |
| `conditions[].expression` | string | CEL expression that returns a boolean. |

Sluice checks every expression at load. A value alone, such as `query.has_cte`, has no fixed type and is a load error. Write `query.has_cte == true`.

### Variables

| Variable | Type | Value |
|---|---|---|
| `subject.id`, `subject.email`, `subject.issuer` | text | Id, email and issuer of the subject. |
| `subject.groups` | list | Groups of the subject. |
| `subject.claims` | map | Claims of the subject. |
| `subject.auth_method` | text | `jwt` or `api_key`. |
| `query.tables` | list | Tables as `catalog.schema.table`, lower case. |
| `query.catalogs` | list | Catalogs that the query reads. |
| `query.joins` | number | Count of joins. |
| `query.has_limit`, `query.limit` | boolean, number | The query has a `LIMIT`, and its value. |
| `query.has_where`, `query.where_columns` | boolean, list | The query has a `WHERE`, and its columns. |
| `query.is_aggregate`, `query.group_by_columns` | boolean, list | The query aggregates, and its `GROUP BY` columns. |
| `query.has_select_star`, `query.has_order_by`, `query.has_union` | boolean | The query has `*`, `ORDER BY` or `UNION`. |
| `query.has_cte`, `query.is_recursive_cte` | boolean | The query has `WITH` or `WITH RECURSIVE`. |

The rules of a [`QueryRejectPolicy`](guardrails.md) use the same variables.

## More examples

### Guard a claim lookup

```yaml
--8<-- "examples/conditions/policies.d/allow-reports-gold.yaml:2"
```

```yaml
--8<-- "examples/conditions/tests/conditions.yaml:claim"
```

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
--8<-- "examples/conditions/steps/01-explain-error.sh"
```

```text
--8<-- "examples/conditions/expected/01-explain-error.txt"
```

## Limits

- No variable holds the current time.
- Except for `tables` and `catalogs`, the `query` values describe the outer `SELECT` only.
- A row filter has its own CEL form. See [Row filters](row-filters.md).

## See also

- [Matching](matching.md)
- [Rejects and rewrites](guardrails.md)
- [Testing policies](testing.md)
