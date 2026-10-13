<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Matching

`spec.match` says which subjects and tables a policy covers. `spec.exclude` takes some of them out again.

## Example

Analysts read `orders`. Support agents read `tickets`.

```yaml
--8<-- "examples/matching/policies.d/allow-reporting.yaml:2"
```

The [suite](testing.md):

```yaml
--8<-- "examples/matching/tests/any.yaml:2"
```

```bash
--8<-- "examples/matching/steps/01-test-any.sh"
```

```text
--8<-- "examples/matching/expected/01-test-any.txt"
```

**What happened:** under `any`, one matching clause is enough. In a clause, `subjects` and `resources` must both match.

## Fields

| Field | Type | Default | Meaning |
|---|---|---|---|
| `any` | list | none | Clauses. One must match. |
| `all` | list | none | Clauses. All must match. |
| `subjects` | object | every subject | The subjects of a clause. |
| `resources` | object | every table | The tables of a clause. |

With both `any` and `all`, one `any` clause or all `all` clauses must match.

A [selector](../reference/glossary.md) works per table or per query:

| Kind | `match` | `exclude` |
|---|---|---|
| `SqlAccessPolicy`, `RowFilterPolicy`, `ColumnMaskPolicy` | Covers each table that matches. | Removes single tables. |
| `QueryRejectPolicy`, `QueryRewritePolicy`, `ApprovalPolicy` | One matching table is enough. | One excluded table switches the policy off. |

### Subjects

Every field that you set must match.

| Field | Matches when |
|---|---|
| `groups` | The subject is in one of the groups. |
| `roles` | The `roles` claim holds one of the roles. Without the claim, the groups count. |
| `apiKeys` | The subject used an API key and its [subject id](subjects.md) is listed. |
| `jwtClaims` | Every claim check passes. |

A check has `claim`, `op` and a value. A dot in `claim` steps into a nested claim.

| `op` | Passes when |
|---|---|
| `Equals` | The claim equals `value`. |
| `NotEquals` | The claim is missing or differs from `value`. |
| `In` | The claim equals one of `values`. |
| `NotIn` | The claim is missing or equals none of `values`. |
| `Exists` | The claim is present. |
| `Matches` | The claim is text that matches the regular expression `pattern`. |

Types must agree: the number `3` does not equal the text `"3"`.

### Resources

| Field | Matches |
|---|---|
| `catalogs`, `schemas`, `tables` | One part each of `catalog.schema.table`. Case does not matter. |
| `columns` | Column names. Only [column masks](column-masks.md) use it. |
| `tags` | Tables and columns with a [tag](classification.md). |
| `actions` | The statement type. See [Access control](access-control.md). |

A pattern must match the whole name.

| Pattern | Meaning |
|---|---|
| `*` | Any text without a dot. |
| `**` | Any text. |
| `\*`, `\.` | A literal `*` or dot. |

## More examples

### All clauses must match

A subject needs both groups.

```yaml
--8<-- "examples/matching/policies.d/allow-invoices.yaml:2"
```

```yaml
--8<-- "examples/matching/tests/all.yaml:all"
```

### Roles and claims

Auditors from Germany or Austria read `audit_notes`.

```yaml
--8<-- "examples/matching/policies.d/allow-auditors.yaml:2"
```

```yaml
--8<-- "examples/matching/tests/subjects.yaml:auditors"
```

### Wildcards

Analysts read some `lake` tables.

```yaml
--8<-- "examples/matching/policies.d/allow-lake.yaml:2"
```

```yaml
--8<-- "examples/matching/tests/resources.yaml:wildcards"
```

### Exclude a group

The `sre` group reads `email` without the mask.

```yaml
--8<-- "examples/matching/policies.d/mask-email.yaml:2"
```

```yaml
--8<-- "examples/matching/tests/exclude.yaml:group"
```

### Which policy wins

One mask applies per column: the one with the highest [priority](index.md). At equal priority, the mask with more names without a wildcard wins. Other kinds have no such rule. This mask has the priority of `mask-email`:

```yaml
--8<-- "examples/matching/policies.d/mask-email-customers.yaml:2"
```

```yaml
--8<-- "examples/matching/tests/priority.yaml:masks"
```

## Limits

- These are load errors: an empty `spec.match`, an unknown tag, a dot in a `catalogs`, `schemas` or `tables` pattern.
- An unknown `op` or `actions` value loads, but it never matches.
- In SQL, a table without catalog matches only `*` and `**` in `catalogs` and `schemas`.
- A claim name that holds a dot cannot be checked.
- An `exclude` with `all` on a `SqlAccessPolicy` works per query, not per table.

## See also

- [Access control](access-control.md)
- [Conditions](conditions.md)
- [Break-glass access](../cookbook/break-glass.md)
