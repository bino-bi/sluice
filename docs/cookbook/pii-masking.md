<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# PII masking

PII is personal data, such as an email address. Analysts query a customer table, but they never see the real `email`, `phone` or `ssn`.

## Example

Start the server with `examples/pii-masking/serve.sh`. Each column has its own column mask. This one returns `NULL` for `email`.

```yaml
--8<-- "examples/pii-masking/policies.d/mask-email.yaml:2"
```

The masks on `phone` and `ssn` select their column in the same way. Both return a constant.

```yaml
--8<-- "examples/pii-masking/policies.d/mask-phone.yaml:mask"
```

```yaml
--8<-- "examples/pii-masking/policies.d/mask-ssn.yaml:mask"
```

The analyst key reads the table.

```bash
--8<-- "examples/pii-masking/steps/01-masked.sh"
```

```json
--8<-- "examples/pii-masking/expected/01-masked.txt"
```

**What happened:** Sluice replaced the table with a subquery that returns the masked values. `birth_date` has no mask, so it comes back unchanged.

This suite shows the rewritten SQL.

```yaml
--8<-- "examples/pii-masking/tests/masks.yaml:2"
```

## More examples

A filter on a masked column sees the masked value. This search for `alice` finds nothing.

```bash
--8<-- "examples/pii-masking/steps/02-filter-on-masked-column.sh"
```

```json
--8<-- "examples/pii-masking/expected/02-filter-on-masked-column.txt"
```

`GROUP BY` on a masked column groups the masked value.

```bash
--8<-- "examples/pii-masking/steps/03-group-by-masked-column.sh"
```

```json
--8<-- "examples/pii-masking/expected/03-group-by-masked-column.txt"
```

## Limits

- A mask without `resources.columns` masks nothing. Sluice gives no warning.
- Some mask types, for example `fake`, run after the query. A filter, join or `GROUP BY` on such a column fails with `ERR_MASK_UNSUPPORTED_CONTEXT`. See [Column masks](../policies/column-masks.md).

## See also

- [Column masks](../policies/column-masks.md)
- [Testing policies](../policies/testing.md#schema-fixtures-for-masked-tables): the `schema:` block for masked tables
- [Break-glass access](break-glass.md)
