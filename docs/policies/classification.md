<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Classification tags

A `DataClassification` puts tags on tables and columns. Other policies then match on a tag instead of on names.

## Example

Goal: mask every column that has the tag `pii`.

The classification tags two columns of `customers`. This kind needs `apiVersion: sluice.bino.bi/v1beta1`.

```yaml
--8<-- "examples/classification/policies.d/classification.yaml:2"
```

The mask policy names no table and no column. It matches the tag.

```yaml
--8<-- "examples/classification/policies.d/mask-pii.yaml:2"
```

```bash
--8<-- "examples/classification/steps/01-customers.sh"
```

```json
--8<-- "examples/classification/expected/01-customers.txt"
```

The table `suppliers` also has an `email` column, but no tag.

```json
--8<-- "examples/classification/expected/02-suppliers.txt"
```

**What happened:** A tag does nothing by itself. When Sluice loads the policies, each tag in a selector becomes the tables of its rules. The mask also took its columns from the `pii` rule.

## Fields

| Field | Meaning |
|---|---|
| `rules[].resources` | `catalogs`, `schemas`, `tables` and `columns`, with wildcards. No `tags`, no `actions`. |
| `rules[].tags` | One or more tags. Allowed: lower-case letters, digits, `.`, `_`, `-`. First and last character: letter or digit. |

You can use `resources.tags` in access, row filter, mask, reject, rewrite and approval policies. One tag of the list must match. Name fields in the same clause must match too. The order of the files does not matter.

## More examples

A deny policy matches the tag `restricted` and leaves out the group `auditors`.

```yaml
--8<-- "examples/classification/policies.d/deny-restricted.yaml:2"
```

```bash
--8<-- "examples/classification/steps/03-payroll.sh"
```

```json
--8<-- "examples/classification/expected/03-payroll.txt"
```

## Limits

- A tag that no `DataClassification` defines stops the load with `unknown tag`. On a reload, the old policies stay active. `sluice policy validate` reports it.
- A rule with only `columns` matches every table. An `effect: allow` by such a tag does not load.
- A `RelationshipPolicy` with tags stops the server start.

## See also

- [Matching](matching.md)
- [Column masks](column-masks.md)
- [Access control](access-control.md)
