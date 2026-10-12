<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Policies

A policy is one YAML object. Sluice denies every query that no policy allows.

## Example

This policy lets the `analysts` group read two tables.

```yaml
--8<-- "examples/policies-basics/policies.d/allow-analysts.yaml:2"
```

Check the policy directory:

```bash
--8<-- "examples/policies-basics/steps/01-validate.sh"
```

```text
--8<-- "examples/policies-basics/expected/01-validate.txt"
```

The file in `invalid` has `effect: Allow`, with a capital letter:

```bash
--8<-- "examples/policies-basics/steps/02-validate-invalid.sh"
```

```text
--8<-- "examples/policies-basics/expected/02-validate-invalid.txt"
```

Exit status 3 means an invalid policy. Without `--strict`, Sluice ignores unknown fields.

## Policy kinds

| Kind | Purpose |
|---|---|
| [`SqlAccessPolicy`](access-control.md) | Allows or denies access to tables. |
| [`RowFilterPolicy`](row-filters.md) | Limits the rows a subject reads. |
| [`ColumnMaskPolicy`](column-masks.md) | Replaces the values of a column. |
| [`QueryRejectPolicy`](guardrails.md) | Rejects a query that matches a rule. |
| [`QueryRewritePolicy`](guardrails.md) | Limits rows, samples rows or sets a timeout. |
| [`ApprovalPolicy`](approvals.md) | Holds a query until a person accepts it. |
| [`SubjectBinding`](subjects.md) | Defines subjects from API keys or JWTs. |
| [`DataSource`](../operations/data-sources.md) | Attaches a database or file store. |
| [`DataClassification`](classification.md) | Puts tags on tables and columns. |
| [`RelationshipPolicy`](rebac.md) | Asks OpenFGA about a table. |

The last two kinds use `apiVersion: sluice.bino.bi/v1beta1`, all others `sluice.bino.bi/v1alpha1`. Another value is a load error.

## Shared fields

| Field | Default | Meaning |
|---|---|---|
| `metadata.name` | required | Lower case letters, digits and `-`. Unique per kind. |
| `metadata.priority` | `0` | 0 to 1000. The highest comes first, then the name decides. |
| `spec.match` | required | Subjects and tables the policy covers. See [Matching](matching.md). |
| `spec.exclude` | none | Subjects or tables to leave out. |
| `spec.conditions` | none | CEL expressions that must all be true. See [Conditions](conditions.md). |
| `spec.enforcementMode` | `Enforce` | `Enforce`, `Audit` or `DryRun`. |

The first six kinds have the `spec` fields. `RelationshipPolicy` has no `conditions`.

## Enforcement mode

`Audit` and `DryRun` do the same: the policy matches but does not enforce. Example:

```yaml
--8<-- "examples/policies-basics/policies.d/deny-customers-audit.yaml:2"
```

```bash
--8<-- "examples/policies-basics/steps/03-explain.sh"
```

```json
--8<-- "examples/policies-basics/expected/03-explain.txt"
```

The decision is `allow`. The deny shows only under `shadow`, here and in the [admin API](../reference/admin-api.md).

## Loading

- Sluice reads all `*.yaml` and `*.yml` files under `policies.directory` (default `./policies.d`), also in subfolders.
- It skips `tests`, `testdata` and folders that start with a dot.
- One file can hold many objects, separated by `---`.
- With an empty policy directory, Sluice denies every query.
- A query needs at least a `DataSource`, a `SubjectBinding` and an allow.

## Limits

- A failing condition still denies the query in `Audit` and `DryRun` mode: `ACL_DENIED`.
- `--strict` does not check the keys under `spec.mask.args`.
- A missing path validates as 0 objects, exit status 0.

## See also

- [Policy schema](../reference/policy-schema.md)
- [Testing policies](testing.md)
- [Policy engines](../architecture/policy-engines.md)
