<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Row filters

A `RowFilterPolicy` limits the rows that a subject reads from a table. The filter applies to every read of the table, also in joins, subqueries, CTEs and set operations.

## Example

Goal: each sales rep reads only their own orders. The table has five orders:

```sql
--8<-- "examples/row-filters/seed.sql:rows"
```

```yaml
--8<-- "examples/row-filters/policies.d/filter-own-orders.yaml:2"
```

```bash
--8<-- "examples/row-filters/steps/01-ana.sh"
```

```json
--8<-- "examples/row-filters/expected/01-ana.txt"
```

A [suite](testing.md) case shows the rewritten SQL:

```yaml
--8<-- "examples/row-filters/tests/filters.yaml:own"
```

**What happened:** Sluice replaced the table with a subquery that holds the filter. The subject id is the bound parameter `$1`, never part of the SQL text. Ana gets only her two orders.

## Fields

| Field | Type | Default | Meaning |
|---|---|---|---|
| `spec.filter.predicate` | object | | The filter as a tree of column checks. |
| `spec.filter.expression` | string | | The filter as a [CEL](conditions.md) expression. Set this or `predicate`, not both. |
| `spec.combine` | string | `restrictive` | How this filter joins other filters on the same table. |

Shared fields: [Policies](index.md). Selectors: [Matching](matching.md). Table names match in any letter case.

### Predicate

A check has a `column`, an `op` and a `value` or `values`. Group checks with `all`, `any` and `not`.

| `op` | Takes | SQL |
|---|---|---|
| `Equals`, `NotEquals` | `value` | `col = $1`, `col <> $1` |
| `GreaterThan`, `GreaterThanOrEqual` | `value` | `col > $1`, `col >= $1` |
| `LessThan`, `LessThanOrEqual` | `value` | `col < $1`, `col <= $1` |
| `Between` | `values`, exactly 2 | `col BETWEEN $1 AND $2` |
| `In`, `NotIn` | `values`, 1 or more | `col IN ($1, $2)`, `col NOT IN ($1)` |
| `Like`, `NotLike` | `value` | `col ~~ $1`, `col !~~ $1`, as SQL `LIKE` |
| `StartsWith`, `EndsWith`, `Contains` | `value` | `starts_with(col, $1)` and so on. `%` and `_` are plain text. |
| `Matches` | `value` | `regexp_matches(col, $1)`. A partial match counts. |

### Template values

A `value` can be a template that Sluice fills for each request.

| Template | Value |
|---|---|
| `{{ subject.sub }}` | Subject id |
| `{{ subject.issuer }}`, `{{ subject.email }}` | Issuer and email. Empty text when not set. |
| `{{ subject.auth_method }}` | `jwt` or `api_key` |
| `{{ subject.claims.<path> }}`, `{{ subject.jwt.<path> }}` | A claim. Dots reach nested claims. |
| `{{ subject.<name> }}` | Short form for a claim, for example `{{ subject.tenantId }}` |

A template must be the whole value, or the policy does not load.

## More examples

### Nested checks

```yaml
--8<-- "examples/row-filters/policies.d/filter-support.yaml:predicate"
```

```yaml
--8<-- "examples/row-filters/tests/filters.yaml:nested"
```

### CEL expression

`row.<column>` is a column. `subject.<path>` reads the same values as a template.

```yaml
--8<-- "examples/row-filters/policies.d/filter-region.yaml:filter"
```

```yaml
--8<-- "examples/row-filters/tests/filters.yaml:cel"
```

You can use comparisons, `&&`, `||`, `!`, `row.x in [...]` and `startsWith`, `endsWith`, `contains` with a text literal. Anything else is a load error.

### Several filters on one table

Filters join in order of priority, highest first. Each filter joins the earlier ones with its own `combine`: `restrictive` is `AND`, `permissive` is `OR`.

```yaml
--8<-- "examples/row-filters/policies.d/filter-open-orders.yaml:2"
```

Ben is in both groups, `sales` and `leads`:

```bash
--8<-- "examples/row-filters/steps/02-ben.sh"
```

```json
--8<-- "examples/row-filters/expected/02-ben.txt"
```

```yaml
--8<-- "examples/row-filters/tests/filters.yaml:permissive"
```

## SQL forms refused on filtered or masked tables

On a table with a row filter or column mask, Sluice refuses these forms. A query with only a limit or sample may use the first four.

| Form | Error code | Write instead |
|---|---|---|
| `EXPLAIN` | `ERR_UNSUPPORTED_SYNTAX` | Not available |
| The text `sluice_src` | `ERR_UNSUPPORTED_SYNTAX` | Another name |
| Schema before a column: `main.orders.id` | `ERR_UNSUPPORTED_SYNTAX` | `orders.id` |
| A table without alias named like a DuckDB keyword: `FROM shop.main.show` | `ERR_UNSUPPORTED_SYNTAX` | `FROM shop.main.show s` |
| `**` or `//` beside another arithmetic operator | `ERR_UNSUPPORTED_SYNTAX` | `pow(a, b)`, `divide(a, b)` |
| A condition in parentheses before `IS NULL`, `IN`, `BETWEEN`, `= ANY` | `ERR_UNSUPPORTED_SYNTAX` | `coalesce(a OR b) IS NULL` |
| Lambda with a comparison: `x -> x > 1` | `ERR_UNSUPPORTED_SYNTAX` | `x -> (x > 1)` |
| `->>` beside `AND`, `OR`, `NOT` | `ERR_UNSUPPORTED_SYNTAX` | `(j ->> 'a') = '1'` |
| `EXTRACT(days FROM d)` | `ERR_UNSUPPORTED_SYNTAX` | `EXTRACT(day FROM d)` |
| `ARRAY[[1, 2], [3, 4]]` | `ERR_UNSUPPORTED_SYNTAX` | `ARRAY[ARRAY[1, 2], ARRAY[3, 4]]` |
| Quoted DuckDB keyword: `WITH "at" AS` | `ERR_UNSUPPORTED_SYNTAX` | Another name |
| PostgreSQL keyword as a named argument: `struct_pack("default" := id)` | `ERR_UNSUPPORTED_SYNTAX` | Another name |
| Bare keyword alias: `SELECT 1 at` | `ERR_SYNTAX` | `SELECT 1 AS "at"` |
| `0x1f`, `0o17`, `0b11` | `ERR_SYNTAX`. At the end of a select item: `ERR_UNSUPPORTED_SYNTAX` | The decimal number |
| `INTERVAL '1 02' DAY TO HOUR` | `ERR_SYNTAX` | `INTERVAL '1 day 2 hours'` |
| `E'\u0041'`, `U&'\0041'` | `ERR_SYNTAX` | The character itself |

```bash
--8<-- "examples/row-filters/steps/03-refused-form.sh"
```

```json
--8<-- "examples/row-filters/expected/03-refused-form.txt"
```

On such a table, an alias comes back in lower case when the query also spells the word another way. `EXTRACT(MONTH FROM d) AS Month` returns `month`. Quote the alias to keep its spelling: `AS "Month"`.

## Limits

- A missing template value fails the query with HTTP 500 and `ERR_INTERNAL`.
- Sluice does not check the filter column. A table without it fails with `ERR_QUERY_FAILED`.
- `combine` must be lower case. Any other value acts as `restrictive`.
- Write table names with three parts. `shop.orders` is rejected with `ACL_REJECTED`.

## See also

- [Multi-tenant isolation](../cookbook/multi-tenant.md)
- [Column masks](column-masks.md)
- [Testing policies](testing.md)
