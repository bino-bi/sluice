<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Column masks

A `ColumnMaskPolicy` replaces the values of a column before the client gets them. Use it for personal data that a subject must not read in full.

## Example

Goal: analysts see only the last four characters of a phone number.

```yaml
--8<-- "examples/column-masks/policies.d/mask-phone.yaml:2"
```

```bash
--8<-- "examples/column-masks/steps/01-phone.sh"
```

```json
--8<-- "examples/column-masks/expected/01-phone.txt"
```

**What happened:** Sluice read the table through a subquery that masks `phone`. The column keeps its name. The rest of the query sees only the masked value, also in `WHERE` and `ORDER BY`.

## Fields

| Field | Type | Default | Meaning |
|---|---|---|---|
| `resources.columns` in `spec.match` | list | | The columns to mask. Without it, the policy masks nothing. |
| `spec.mask.type` | string | | A mask type from the table below. |
| `spec.mask.args` | object | | The options of the mask type. |

Shared fields: [Policies](index.md). Selectors: [Matching](matching.md).

- Column names match in any letter case.
- A selector with `columns` and no `tables` masks the column in every table.
- A selector with `tags` takes the columns from a `DataClassification`. See [Classification tags](classification.md). Sluice still needs the column list of the table.
- A row filter on the same table tests the real value.

## Mask types

A mask runs in one of two places. "SQL" means the mask is part of the rewritten SQL. "Post-query" means Sluice changes the result rows after the query.

| Type | Result | `args` | Runs |
|---|---|---|---|
| `null` | `NULL` | None. YAML needs quotes: `type: "null"` | SQL |
| `constant` | A fixed value | `value`: required. Text, number or boolean. A list shows as text such as `[1 2]` | SQL |
| `partial` | The first and last characters. `maskChar` replaces the rest. | `showFirst`, `showLast`: default 0, one must be above 0. `maskChar`: default `*` | SQL |
| `hash` | A SHA-256 hash as hex text | `algorithm`: `sha256` or `hmac_sha256`, default `sha256`. `saltRef`: a `secret://` reference to the salt, required for `hmac_sha256` | SQL for `sha256`, post-query for `hmac_sha256` |
| `regex` | The text with every match of `pattern` replaced | `pattern`: required, a regular expression in RE2 syntax, at most 512 bytes. `replacement`: default empty | SQL |
| `truncate` | The first `length` characters | `length`: required, 1 or more. `suffix`: text to add after a cut | SQL |
| `fpe` | The characters of the alphabet, encrypted. Other characters stay. | `keyRef`: required, a `secret://` reference to the key. `alphabet`: `numeric`, `lower` or `alphanumeric`, default `numeric`. `customAlphabet`: 2 to 36 characters. `tweak`: hex, at most 16 bytes | Post-query |
| `jitter` | The number, moved by up to `range` | `range`: required, between 0 and 1. 0.1 means 10 percent. `seed` | Post-query |
| `fake` | An invented value | `fakeType`: required, one of `name`, `first_name`, `last_name`, `email`, `phone`, `company`, `city`, `country`, `uuid`. `seed` | Post-query |

- `hash`, `fpe`, `jitter` and `fake` give the same output for the same input.
- `seed` is plain text or a `secret://` reference. See [Server config](../operations/server-config.md) for secrets.
- Another `type` and most wrong `args` fail `sluice policy validate`. So does a `saltRef` or `keyRef` that is not a `secret://` reference. It does not check the type of `value`. Unknown keys in `args` pass.

## More examples

### One column per mask type

The table `employees` has two rows:

```sql
--8<-- "examples/column-masks/seed.sql:rows"
```

`mask-phone` from the first example masks `phone`. These policies mask the other columns:

```yaml
--8<-- "examples/column-masks/policies.d/masks.yaml:2"
```

The secrets come from the environment:

```bash
--8<-- "examples/column-masks/serve.sh:secrets"
```

```bash
--8<-- "examples/column-masks/steps/02-all-masks.sh"
```

```json
--8<-- "examples/column-masks/expected/02-all-masks.txt"
```

**What happened:** every column shows its mask. `card` keeps its format. `salary` stays within 10 percent of the real number.

A [suite](testing.md) shows where each mask runs:

```yaml
--8<-- "examples/column-masks/tests/masks.yaml:all"
```

A `sha256` mask with `saltRef` also needs its secret when the suite runs.

### Several masks on one column

One mask wins. Masks never stack. Sluice picks the higher `priority`, then the more specific selector, then the first name in alphabetical order.

`hide-phone` also masks `phone`. `mask-phone` from the first example has the higher priority and wins:

```yaml
--8<-- "examples/column-masks/policies.d/hide-phone.yaml:2"
```

```yaml
--8<-- "examples/column-masks/tests/masks.yaml:one-mask"
```

## SQL forms refused on a post-query mask

A column with a post-query mask can only be a plain item of the outer select list.

| Refused form | Error code |
|---|---|
| The masked column in `WHERE`, `GROUP BY`, `ORDER BY`, a join condition, an expression, a subquery or a CTE | `ERR_MASK_UNSUPPORTED_CONTEXT` |
| `SELECT *` inside or over a subquery or a CTE. List the columns. | `ERR_MASK_UNSUPPORTED_CONTEXT` |
| `SELECT DISTINCT` with the masked column. Its alias or position in another clause, for example `ORDER BY 2` | `ERR_MASK_UNSUPPORTED_CONTEXT` |
| `USING` and `NATURAL` joins, the table alias as a value, a column alias list, `#3`, a column with its schema | `ERR_MASK_UNSUPPORTED_CONTEXT` |

```bash
--8<-- "examples/column-masks/steps/03-post-query-mask-in-where.sh"
```

```json
--8<-- "examples/column-masks/expected/03-post-query-mask-in-where.txt"
```

Every masked table also refuses `EXPLAIN` and some DuckDB forms. See [SQL forms refused on filtered or masked tables](row-filters.md#sql-forms-refused-on-filtered-or-masked-tables).

## Errors on a masked table

| Cause | Result |
|---|---|
| The table does not exist | `ERR_REWRITE_FAILED` |
| Sluice cannot read the data source | `ERR_DATASOURCE_UNAVAILABLE` |
| A column changed after the last reload | `ERR_QUERY_FAILED` |
| A comparison needs the column type from before the mask | `ERR_QUERY_FAILED` |
| The query reads `rowid` | `ERR_QUERY_FAILED` |
| A secret is missing | HTTP 500, `ERR_INTERNAL` |
| The `fpe` key is not 16, 24 or 32 bytes long | HTTP 500, `ERR_INTERNAL` |
| `jitter` on a text column, `fpe` on a very short value | HTTP 200 with a cut JSON body |

## Limits

- Sluice keeps the column list of a masked table until a [reload](../operations/hot-reload.md). Reload after a column changes.
- `sluice policy validate` does not read secrets.
- A `sha256` hash without `saltRef` can be guessed for common values. Use `hmac_sha256` or set `saltRef`.

## See also

- [PII masking](../cookbook/pii-masking.md)
- [Classification tags](classification.md)
- [Testing policies](testing.md#schema-fixtures-for-masked-tables)
