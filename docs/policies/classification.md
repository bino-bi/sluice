<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Data classification

`DataClassification` (apiVersion `sluice.bino.bi/v1beta1`) assigns tags to resources so
enforcement policies can match on *what data is* instead of *what it is called*. Declare once
that certain columns are `pii`; every mask, filter, deny, or approval that selects
`resources: { tags: ["pii"] }` follows the classification automatically — including tables added
to it later.

## The kind

```yaml
apiVersion: sluice.bino.bi/v1beta1
kind: DataClassification
metadata: { name: pii-catalog }
spec:
  rules:
    - resources:
        tables: ["**.customers"]
        columns: ["email", "phone"]
      tags: ["pii"]
```

- `spec.rules[]` each pair a resource selector with one or more tags. At least one tag is
  required per rule.
- Tag syntax is strict: lowercase letters and digits at the start and end, with dots,
  underscores, and hyphens allowed inside (`pii.contact`, `finance-restricted`).
- A rule's `resources` block uses the ordinary fields `catalogs`, `schemas`, `tables`,
  `columns` with the usual [wildcards](matching.md) — but it may **not** contain `tags` (no
  recursion) or `actions`. Either is a validation error.

## How tags resolve

Classifications are consumed at **policy compile time**, not per query. On every load or reload,
all rules are folded into a tag → resource index; each selector that references
`resources.tags` is then expanded against that index. Consequences:

- Within one `tags` list, tags are OR'd: the clause matches a table when *any* referenced
  classification rule matches it. Name constraints in the same clause (`catalogs`, `tables`, …)
  still apply on top (AND).
- Referencing a tag that no `DataClassification` defines is a **compile error**
  (`resource.tags: unknown tag …`) — the whole snapshot is rejected and, on hot reload, the
  previous one stays active. A typo'd tag can never silently protect nothing.
- File order does not matter; the index is built before any policy compiles.

For [column masks](column-masks.md) the classification also supplies the columns: a
`ColumnMaskPolicy` matching by tag masks exactly the columns listed in the matching
classification rules — no separate `resources.columns` needed on the mask itself.

## Governance pattern

Because classification and enforcement are separate objects, they can have separate owners in
the same `policies.d/` tree: the data-governance team maintains `classifications/*.yaml`
(what is `pii`, what is `finance-restricted`), while the platform team maintains the enforcement
policies that reference those tags. CI runs `sluice policy validate --strict` on the merged
directory, so an enforcement policy referencing a tag that governance removed fails the build
instead of the runtime.

## Recipes

**The worked pair** — governance tags contact columns on every `customers` table; one mask
enforces it everywhere:

```yaml
apiVersion: sluice.bino.bi/v1beta1
kind: DataClassification
metadata: { name: pii-catalog }
spec:
  rules:
    - resources:
        tables: ["**.customers"]
        columns: ["email", "phone"]
      tags: ["pii"]
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: mask-pii, priority: 50 }
spec:
  match:
    any:
      - resources: { tags: ["pii"] }
  mask:
    type: partial
    args: { showFirst: 1, showLast: 0 }
```

A query touching `shop.main.customers` gets `email` and `phone` masked; a table outside the
classification is untouched even though the mask policy names no tables.

**Hard-deny restricted data for everyone except auditors:**

```yaml
apiVersion: sluice.bino.bi/v1beta1
kind: DataClassification
metadata: { name: finance-catalog }
spec:
  rules:
    - resources:
        schemas: ["finance"]
        tables: ["ledger", "payroll"]
      tags: ["finance-restricted"]
---
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata: { name: deny-finance-restricted, priority: 900 }
spec:
  effect: deny
  match:
    any:
      - resources: { tags: ["finance-restricted"] }
  exclude:
    any:
      - subjects: { groups: ["auditors"] }
  message: "finance-restricted data requires auditor group membership"
```
