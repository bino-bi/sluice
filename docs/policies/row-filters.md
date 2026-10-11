<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Row filters

`RowFilterPolicy` narrows which rows a subject can see. At rewrite time the gateway wraps every
matched table in a subquery carrying the filter as a **parameterized WHERE clause**. Values bind
as `$N` positional parameters and never appear in the SQL text:

```sql
-- client sends
SELECT id FROM shop.main.orders
-- sluice executes ($1 = "t-42")
SELECT id FROM (SELECT * FROM shop.main.orders sluice_src WHERE sluice_src.tenant_id = $1) orders
```

The wrap happens on the parsed AST and covers every occurrence of
the table in the statement: joins, FROM subqueries, CTE bodies, set-operation arms, and
subqueries inside expressions (select list, `WHERE`, `HAVING`, `JOIN ... ON`, `ORDER BY`,
`LIMIT`, `VALUES`). Table names are matched without regard to letter case, also when quoted. Write tables of an
attached source with all three parts (`shop.main.orders`): the two-part form `shop.orders` is
refused with `ACL_REJECTED`, because DuckDB reads it as catalog and table while policies read it
as schema and table.
The original alias is preserved so outer column references keep resolving.
A filtered table without an alias gets a wrapper named after the table. When that lower-case
name is one DuckDB reads as a keyword (`semi`, `anti`, `asof`, `positional`, `by`, `qualify`,
...), the query is refused with `ERR_UNSUPPORTED_SYNTAX`. Give the table an alias.
Inside the subquery the table is named `sluice_src`, and the filter columns are qualified with
that name. The name is reserved: a statement that contains `sluice_src` is refused with
`ERR_UNSUPPORTED_SYNTAX`. Make sure each filter column exists in every table the filter matches,
also under a wildcard table pattern: Sluice does not check it.
When the table also has a column mask, the same subquery lists the table's columns and applies
the masks (see [Column masks](column-masks.md)).

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
  match: { any: [{ resources: { catalogs: ["shop"], schemas: ["main"], tables: ["customers"] } }] }
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

## SQL forms refused on filtered or masked tables

When a row filter, column mask, `limit` or `sample` applies to a `SELECT`, Sluice changes the
parsed statement and writes it back as SQL. The parser and the writer follow PostgreSQL, but
DuckDB runs the text, and a few forms mean something else in DuckDB. So before Sluice adds any
policy SQL, DuckDB parses your text and the rewritten text. The query is refused:

- with `ERR_UNSUPPORTED_SYNTAX` ("the statement would change meaning when rewritten for row
  filters, masks or limits") when DuckDB reads the two texts in another way;
- with `ERR_SYNTAX` ("DuckDB cannot parse the statement") when DuckDB cannot parse your text.

A `QueryRewritePolicy` limit or sample that matches every query makes this apply to every
`SELECT`. A query that no filter, mask, limit or sample applies to runs your text as written and
is not checked.

| Refused form | Example | Write instead |
| ------------ | ------- | ------------- |
| Parentheses the rewrite drops: around `AND`, `OR`, `NOT` or `IS DISTINCT FROM` before `IS [NOT] NULL`, around `IS DISTINCT FROM` before `IS TRUE`, and around a comparison, `AND` or `OR` before `IN`, `BETWEEN` or `= ANY`, or as a `BETWEEN` bound | `(a OR b) IS NULL`, `(NOT flag) IS NULL`, `(a = 1) IN (false)` | Wrap the operand in `coalesce`: `coalesce(a OR b) IS NULL` |
| `**` and `//` | `2 * 3 ** 2`, `b // 2 + 1` | `2 * pow(3, 2)`, `divide(b, 2) + 1` |
| A lambda whose body has a comparison, `AND` or `OR` | `list_filter(l, x -> x > 1)` | Put the body in parentheses: `x -> (x > 1)` |
| `->>` under `AND`, `OR` or `NOT` | `flag AND j ->> 'a' = '1'` | `flag AND (j ->> 'a') = '1'` |
| Hex, octal and binary literals (`ERR_SYNTAX` unless the literal ends a select item) | `0x1f`, `0o17`, `0x1f + 1` | The decimal value: `31` |
| `INTERVAL 'n' <unit>` with a unit pg_query does not know: `WEEK`, `QUARTER`, `DECADE`, `CENTURY`, `MILLENNIUM`, `MILLISECOND`, `MICROSECOND`, and every plural (`DAYS`, `WEEKS`, ...). In `WHERE` pg_query cannot parse it (`ERR_SYNTAX`) | `INTERVAL '3' WEEK` | The unit inside the string, `INTERVAL '3 weeks'`, or `to_weeks(3)` |
| Interval ranges and precisions (`ERR_SYNTAX`) | `INTERVAL '1 02' DAY TO HOUR`, `INTERVAL '3' SECOND(3)` | `INTERVAL '1 day 2 hours'` |
| `EXTRACT` with a plural unit | `EXTRACT(days FROM d)` | The singular unit: `EXTRACT(day FROM d)` |
| `ARRAY[[...]]` | `ARRAY[[1, 2], [3, 4]]` | `ARRAY[ARRAY[1, 2], ARRAY[3, 4]]` |
| A quoted name that only DuckDB reads as a keyword, as a CTE name, column list entry, column reference or window name, and a PostgreSQL keyword as a named argument | `WITH "at" AS (...)`, `struct_pack("default" := id)` | Another name |
| A bare alias that DuckDB reads as a keyword (`ERR_SYNTAX`) | `SELECT 1 at` | `SELECT 1 AS "at"` |
| `U&'...'` strings and `\u` escapes in `E'...'` strings (`ERR_SYNTAX`) | `E'\u0041'` | The character itself, or `E'\x41'` |
| A column qualified with the schema or catalog of a filtered or masked table that has no alias | `SELECT main.orders.id FROM main.orders` | `orders.id`, or give the table an alias |
| A filtered or masked table without an alias whose name DuckDB reads as a keyword (see above) | `SELECT count(*) FROM main."by"` | Give the table an alias |
| The name `sluice_src`, also inside a longer name or a string | `SELECT id AS sluice_src FROM shop.main.orders` | Another name |

Some of these forms give the same rows in both readings (`EXTRACT(days FROM d)`, `ARRAY[[1, 2]]`),
but DuckDB builds another tree for them, so they are refused too.

These forms are rewritten into the form DuckDB builds, so they work: `LIKE ... ESCAPE` and
`ILIKE ... ESCAPE` (also with `NOT`), `INTERVAL 'n' <unit>` with `YEAR`, `MONTH`, `DAY`, `HOUR`,
`MINUTE` or `SECOND`, casts to `json` and `bit`, a bare `FLOAT`, which is `REAL` as in DuckDB, and
`EXTRACT` with a unit that is not a DuckDB keyword, such as `EXTRACT(EPOCH FROM ts)`.
An alias keeps your spelling in result column names, struct keys and `to_json` keys. Write each
name in one spelling: when the same name appears in two spellings, as in
`struct_pack(MyKey := id)` next to `id AS mykey`, both come back in lower case. A keyword, a type
name or an `EXTRACT` unit counts too: `EXTRACT(MONTH FROM d) AS Month`, `ts::date AS Date` and
`EXTRACT(EPOCH FROM ts) AS Epoch` come back as `month`, `date` and `epoch`. Quote such an alias to
keep its spelling: `AS "Month"`. A struct field without an alias comes back in lower case as well:
`SELECT st.Y FROM t` gives a column `y`, where your text run as written gives `Y`. Give it an alias:
`st.Y AS "Y"`.

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
  match: { any: [{ resources: { catalogs: ["shop"], schemas: ["main"], tables: ["customers", "orders"] } }] }
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
  match: { any: [{ resources: { catalogs: ["shop"], schemas: ["main"] } }] }
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
  match: { any: [{ resources: { catalogs: ["shop"], schemas: ["main"], tables: ["orders"] } }] }
  filter:
    expression: "row.region == subject.claims.region"
```
