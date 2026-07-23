<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Row filters

`RowFilterPolicy` narrows which rows a subject can see. At rewrite time the gateway wraps every
matched table in a subquery carrying the filter as a **parameterized WHERE clause** — values bind
as `$N` positional parameters and never appear in the SQL text:

```sql
-- client sends
SELECT id FROM shop.main.orders
-- sluice executes ($1 = "t-42")
SELECT id FROM (SELECT * FROM shop.main.orders WHERE tenant_id = $1) orders
```

The wrap happens on the parsed AST, after `SELECT *` expansion, and covers every occurrence of
the table in the FROM tree — joins, subqueries, and CTE bodies included. The original alias is
preserved so outer column references keep resolving.

A filter carries exactly one of two bodies: a structured predicate tree (`filter.predicate`) or a
CEL expression (`filter.expression`). Both compile to the same parameterized form.

## Structured predicates

A leaf predicate is `column` + `op` + a value set. Operators are **PascalCase**:

| Op | Takes | Rewrites to |
| -- | ----- | ----------- |
| `Equals` / `NotEquals` | `value` | `col = $1` / `col <> $1` |
| `GreaterThan` / `GreaterThanOrEqual` | `value` | `col > $1` / `col >= $1` |
| `LessThan` / `LessThanOrEqual` | `value` | `col < $1` / `col <= $1` |
| `Between` | `values` (exactly 2) | `col BETWEEN $1 AND $2` |
| `In` / `NotIn` | `values` (1 or more) | `col IN ($1, …)` / `col NOT IN ($1, …)` |
| `Like` / `NotLike` | `value` | `col ~~ $1` / `col !~~ $1` (SQL `LIKE` / `NOT LIKE`) |
| `IsNull` / `IsNotNull` | nothing | `col IS NULL` / `col IS NOT NULL` |
| `StartsWith` / `EndsWith` | `value` | `starts_with(col, $1)` / `ends_with(col, $1)` |
| `Contains` | `value` | `contains(col, $1)` |
| `Matches` | `value` | `regexp_matches(col, $1)` |

`StartsWith`, `EndsWith`, and `Contains` compare **literally** — `%`, `_`, and `\` in the value
match themselves, so user-supplied prefixes need no escaping. Only `Like`/`NotLike` interpret
pattern metacharacters. `Matches` uses regular-expression **partial-match** semantics: the filter
keeps a row when the pattern matches anywhere in the value; anchor with `^…$` for a full match.

Internal nodes nest arbitrarily via `all` (AND), `any` (OR), and `not`:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: RowFilterPolicy
metadata: { name: filter-active-eu, priority: 60 }
spec:
  match: { any: [{ resources: { tables: ["shop.main.customers"] } }] }
  filter:
    predicate:
      all:                              # AND
        - column: deleted_at
          op: IsNull
        - any:                          # OR
            - column: region
              op: In
              values: ["eu-west", "eu-central"]
            - column: vip
              op: Equals
              value: true
        - not:                          # NOT
            column: status
            op: Equals
            value: "embargoed"
```

This injects `WHERE deleted_at IS NULL AND (region IN ($1, $2) OR vip = $3) AND NOT status = $4`.

## Template values

A `value` (or `values` entry) that is a `{{ … }}` string resolves per request:

| Template | Resolves to |
| -------- | ----------- |
| `{{ subject.sub }}` | authenticated subject id |
| `{{ subject.issuer }}`, `{{ subject.email }}`, `{{ subject.groups }}` | identity fields |
| `{{ subject.auth_method }}`, `{{ subject.request_id }}` | request identity metadata |
| `{{ subject.jwt.<path> }}`, `{{ subject.claims.<path> }}` | nested claim by dotted path |
| `{{ subject.<name> }}` | shorthand — falls through to the claim of that name |
| `{{ request.remote_ip }}`, `{{ request.user_agent }}`, `{{ request.headers.<name> }}` | request facts |

A template must span the whole value — mixed strings like `"t-{{ subject.sub }}"` are a load
error. The rendered value is appended as a bound `$N` parameter, never spliced into SQL text. If
the variable is missing at query time (claim absent, header not sent), the request is **denied** —
a filter never silently degrades to unfiltered rows.

## CEL expressions

`filter.expression` is the same filter written as CEL over `row`, `subject`, and `request`. The
two policies below are equivalent:

```yaml
# fragment — predicate form
filter:
  predicate:
    column: tenant_id
    op: Equals
    value: "{{ subject.claims.tenant_id }}"
```

```yaml
# fragment — CEL form
filter:
  expression: "row.tenant_id == subject.claims.tenant_id"
```

The supported subset: `row.<col>` compared with `== != < <= > >=` against a literal or a
`subject.*`/`request.*` reference; `&& || !`; `row.<col> in [literals]`; and
`row.<col>.startsWith/endsWith/contains("literal")` (lowered to the `StartsWith`/`EndsWith`/
`Contains` operators above — literal comparison, no pattern escaping involved). Arithmetic,
function calls, macros, `query.*` references, and dynamic string-match arguments are
rejected at load. CEL never renders SQL text — it lowers into the same predicate tree, so
parameter binding and the missing-variable deny apply identically.

## Combining multiple filters

When several filters land on the same table they fold in policy order (priority descending, then
name ascending). Each incoming filter attaches using its own `spec.combine`: `restrictive` (the
default) ANDs it onto what is already there; `permissive` ORs it. With `filter-active-eu` above
plus the tenant filter from [the policy model](index.md), a query against `shop.main.customers`
becomes:

```sql
-- both restrictive: subjects see only their tenant's rows AND the active-EU subset
... WHERE (tenant_id = $1) AND (deleted_at IS NULL AND (...))
-- filter-active-eu with combine: permissive instead: either condition suffices
... WHERE (tenant_id = $1) OR (deleted_at IS NULL AND (...))
```

Use `permissive` only for deliberate carve-outs — an OR widens what each filter alone allows.

## Rolling out with DryRun

Set `enforcementMode: DryRun` to deploy a filter that matches and is recorded as a shadow outcome
(the `shadow` list in `sluice policy explain --json` and in the admin explain endpoint) without
touching any rows. Watch who *would* be filtered, fix selector or predicate mistakes, then flip to
`Enforce`. See [shared spec fields](index.md#shared-spec-fields).

## Recipes

**Tenant isolation** — every subject sees only rows of their own tenant:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: RowFilterPolicy
metadata: { name: filter-tenant, priority: 80 }
spec:
  match: { any: [{ resources: { tables: ["shop.main.customers", "shop.main.orders"] } }] }
  combine: restrictive
  filter:
    predicate:
      column: tenant_id
      op: Equals
      value: "{{ subject.tenantId }}"
```

**Soft-delete hiding** — everyone except auditors sees only live rows:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: RowFilterPolicy
metadata: { name: hide-soft-deleted, priority: 70 }
spec:
  match: { any: [{ resources: { tables: ["shop.main.**"] } }] }
  exclude:
    any:
      - subjects: { groups: ["auditors"] }
  filter:
    predicate:
      column: deleted_at
      op: IsNull
```

**Region scoping via a claim** — rows follow the `region` claim stamped by the JWT or the
subject binding:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: RowFilterPolicy
metadata: { name: filter-region, priority: 60 }
spec:
  match: { any: [{ resources: { tables: ["shop.main.orders"] } }] }
  filter:
    expression: "row.region == subject.claims.region"
```
