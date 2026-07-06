<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Matching and precedence

Every policy carries a `spec.match` selector deciding when it applies. A selector holds clauses
under `any` (OR — one matching clause suffices) or `all` (AND — every clause must hold). Each
clause pairs an optional `subjects` block with an optional `resources` block; within a clause,
both must hold. An empty selector matches **nothing** — a policy without clauses is inert.

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata: { name: allow-reporting, priority: 100 }
spec:
  effect: allow
  match:
    any:
      # Clause 1: analysts may read the reporting schema ...
      - subjects: { groups: ["analysts"] }
        resources: { catalogs: ["shop"], schemas: ["reporting"] }
      # Clause 2: ... OR the export bot may read one table in it.
      - subjects: { apiKeys: ["export-bot"] }
        resources: { catalogs: ["shop"], schemas: ["reporting"], tables: ["exports"] }
```

With `all`, every clause must hold — useful to AND independent subject checks:

```yaml
# fragment — selector only, not a standalone policy
match:
  all:
    - subjects: { groups: ["finance"] }        # must be in finance ...
    - subjects: { ipRanges: ["10.0.0.0/8"] }   # ... AND on the internal network
```

## Subject fields

All populated fields inside one `subjects` block must hold (AND); an empty `subjects` block
matches every subject, including anonymous ones.

| Field | Matches when |
| ----- | ------------ |
| `groups` | The subject has at least one listed group |
| `apiKeys` | The subject authenticated with an API key whose id is listed |
| `roles` | The `roles` claim contains a listed role (falls back to groups if the claim is absent) |
| `ipRanges` | The request's remote IP is inside one of the CIDR ranges |
| `jwtClaims` | Every listed claim check passes |

Each `jwtClaims` entry names a claim (dotted paths descend into nested claims), an `op`, and a
comparison value:

| `op` | Uses | Meaning |
| ---- | ---- | ------- |
| `Equals` | `value` | Claim exists and equals the value |
| `NotEquals` | `value` | Claim is absent or differs |
| `In` | `values` | Claim equals one of the values |
| `NotIn` | `values` | Claim is absent or equals none of the values |
| `Exists` | — | Claim is present, whatever its value |
| `Matches` | `pattern` | Claim is a string matching the regular expression |

!!! note "Claim comparison is type-strict"
    A numeric claim never equals a string policy value: `1` does not match `value: "1"`, and
    `true` does not match `value: "true"`. Write the YAML value in the claim's actual type.

## Resource fields

Tables are identified by their three-part path `catalog.schema.table` — that dotted key is what
you see in audit records and test assertions. The selector matches each segment with its own
field:

| Field | Matches against |
| ----- | --------------- |
| `catalogs` | The catalog segment |
| `schemas` | The schema segment |
| `tables` | The bare table name |
| `columns` | Column names (required for column masks; does not constrain table matching) |
| `tags` | Tables/columns tagged by a [DataClassification](classification.md) |
| `actions` | The statement verb: `SELECT`, `INSERT`, `UPDATE`, `DELETE` |

!!! warning "One segment per field"
    A dotted pattern such as `tables: ["shop.main.orders"]` never matches — it is compared
    against the bare table name. Write `catalogs: ["shop"], schemas: ["main"], tables:
    ["orders"]`. An unqualified query (`FROM orders`) has empty catalog and schema segments,
    which only `*`/`**` patterns match.

A clause matches a query when **at least one** referenced table satisfies its `resources` block —
see [access control](access-control.md) for the consequences on multi-table queries.

## Wildcards

Patterns are anchored — they must match the whole segment.

| Pattern | Meaning | Example |
| ------- | ------- | ------- |
| `*` | Any characters within one dotted segment | `analytics_*` matches `analytics_daily`, not `analytics.daily` |
| `**` | Any characters across segments | `**` in `tables` matches every table name |
| `\*` | A literal asterisk | `total\*` matches only `total*` |

## Priority and specificity

When several policies of the same kind claim the same target, resolution is deterministic:
**priority descending, then selector specificity descending, then name ascending.** Specificity
counts the non-wildcard resource patterns in a clause. Two overlapping column masks:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: hash-email-everywhere, priority: 50 }
spec:
  match:
    any:
      - resources: { tables: ["*"], columns: ["email"] }   # specificity 1
  mask: { type: hash }
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: null-email-customers, priority: 50 }
spec:
  match:
    any:
      - resources: { tables: ["customers"], columns: ["email"] }  # specificity 2
  mask: { type: "null" }
```

Equal priority, so specificity decides: `customers.email` is nulled while every other `email`
column is hashed. Raise `hash-email-everywhere` to `priority: 60` and it takes the column
outright — priority always beats specificity.

## Exclude

`spec.exclude` is a second selector that carves matches back out. Its scope depends on the kind:

- **Whole-query kinds** (`SqlAccessPolicy`, `QueryRejectPolicy`, `QueryRewritePolicy`,
  `ApprovalPolicy`): if `exclude` matches, the policy is dropped for the entire query.
- **Per-table kinds** (`RowFilterPolicy`, `ColumnMaskPolicy`): `exclude` is applied per table, so
  carving out one table never lifts protection from the other tables in a join.

## Combining row filters

When several `RowFilterPolicy` objects hit the same table, their predicates fold according to
`spec.combine`: `restrictive` ANDs them (the default), `permissive` ORs them. Values always bind
as `$N` parameters — never spliced into the SQL text. A tenant and a region filter on `orders`:

```sql
-- before
SELECT id FROM orders
-- after, both filters restrictive
SELECT id FROM (SELECT * FROM orders WHERE tenant_id = $1 AND region = $2) orders
-- after, second filter permissive
SELECT id FROM (SELECT * FROM orders WHERE tenant_id = $1 OR region = $2) orders
```

## Recipes

**Match everyone except a group** — mask a column, but let `sre` through (break-glass):

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: mask-email-except-sre, priority: 50 }
spec:
  match:
    any:
      - resources: { tables: ["customers"], columns: ["email"] }
  exclude:
    any:
      - subjects: { groups: ["sre"] }
  mask: { type: "null" }
```

**Match a whole catalog** — clients must qualify tables (`shop.main.orders`) for the catalog
segment to be present:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata: { name: allow-whole-catalog, priority: 50 }
spec:
  effect: allow
  match:
    any:
      - subjects: { groups: ["analysts"] }
        resources: { catalogs: ["shop"] }
```

**Match by tag** — classify once, select the tag everywhere. An unknown tag is a compile error,
so a typo cannot silently protect nothing:

```yaml
apiVersion: sluice.bino.bi/v1beta1
kind: DataClassification
metadata: { name: pii-map }
spec:
  rules:
    - resources: { tables: ["customers"], columns: ["email", "phone"] }
      tags: ["pii"]
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: hash-all-pii, priority: 40 }
spec:
  match:
    any:
      - resources: { tags: ["pii"] }
  mask: { type: hash }
```
