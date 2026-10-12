<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Column masks

`ColumnMaskPolicy` replaces the values of matched columns before they reach the client. Masks run
at one of two stages:

| Stage | Providers | How |
| ----- | --------- | --- |
| SQL | `null`, `constant`, `partial`, `hash` (sha256), `regex`, `truncate` | applied in a subquery that takes the place of the table in the rewritten query. Plaintext never leaves DuckDB |
| Post-query | `hash` (hmac_sha256), `fpe`, `jitter`, `fake` | applied in Go to each result row as it streams, because the transform needs key material or logic SQL cannot express |

A table with a column mask is read through a subquery that lists the columns of the table and
applies the SQL masks:

```sql
-- client sends (null mask on email, row filter on tenant_id)
SELECT c.email FROM shop.main.customers c
-- sluice executes ($1 = the caller's tenant)
SELECT c.email FROM (SELECT sluice_src.id, sluice_src.name, NULL AS email, sluice_src.tenant_id,
  sluice_src.created_at FROM shop.main.customers sluice_src WHERE sluice_src.tenant_id = $1) c
```

Matching rules to know:

- A mask **matches nothing unless the selector sets `resources.columns`**. A clause with only
  tables silently masks no columns.
- A mask follows the column by name, without regard to letter case. The rest of the statement
  only reads the subquery, so every use of a SQL-masked column gets the masked value: the select
  list, `WHERE`, `GROUP BY`, `ORDER BY`, join conditions, `SELECT *`, the table alias as a value
  (`SELECT c`, `to_json(c)`), column alias lists, `#2`, subqueries and CTEs. A `USING` or
  `NATURAL` join compares the masked values.
- **Every table with a mask needs an entry in the schema cache**, because the subquery lists its
  columns. Without an entry the query is refused with `ERR_REWRITE_FAILED`. When the schema
  cannot be loaded, or the catalog is not attached, the query fails with
  `ERR_DATASOURCE_UNAVAILABLE`. Write the table with all three parts (`shop.main.customers`).
- A column that the schema cache does not list is not read. A reload empties the cache, and a
  catalog is loaded again as a whole on the next lookup that misses one of its tables (a table
  not cached yet, a name that does not exist, an evicted entry). There is no timer. Until that
  load a column added at the source is missing from `*` and a query that names it fails, and a
  column dropped at the source makes every query on the table fail with `ERR_QUERY_FAILED`. With
  `cache.rewrite.enabled`, a repeated query keeps the old column list for up to
  `cache.rewrite.ttl` after that load. Do not rely on a new column staying hidden: add its mask
  before you add the column.
- A mask rule that names only columns, with no tables, applies to every table it matches. Each of
  these tables is read through the subquery and needs a schema cache entry, also a table that
  does not have the column.
- A row filter on the same table sits in the same subquery and tests the raw value. A mask on the
  filter column does not change the filter; the query sees the masked value.
- A masked column read without an alias gets the name of the source column, as an unmasked
  column does: `SELECT "EMAIL"` returns a column named as the table defines it.
- The masked column has the type of the mask output. A `null` mask is a `NULL` without the
  column's type, which DuckDB reads as `INTEGER`. A `constant` mask has the type of its value, so
  `value: "1900-01-01"` on a date column gives a text column. An expression that needs the
  original type can fail: `birth_date < DATE '2000-01-01'` under that constant, or a `USING`
  join of a null-masked text column with a text column. Such a query returns
  `ERR_QUERY_FAILED`.
- `rowid` does not work on a masked table. A column qualified with the table's schema or catalog
  (`main.customers.email`) is refused with `ERR_UNSUPPORTED_SYNTAX`; write `customers.email` or
  use an alias. On a query with a post-query mask the code is `ERR_MASK_UNSUPPORTED_CONTEXT`.
- On a query with a row filter or a mask, the name `sluice_src` is reserved for the table inside
  the subquery. A statement that contains it anywhere, also inside a longer name or a string, is
  refused with `ERR_UNSUPPORTED_SYNTAX`.
- A post-query masked column passes through the subquery raw and is masked in Go. So it can only
  be a bare item of the outermost select list. Anywhere else it is refused with
  `ERR_MASK_UNSUPPORTED_CONTEXT`: inside an expression, in `ORDER BY` or `DISTINCT ON`, and inside
  a subquery or CTE. That includes `SELECT *` in a subquery or CTE over such a table (list the
  columns you need there). The exception is `EXISTS (SELECT * FROM ...)`: a star that is the
  whole select list there is fine, unless the subquery has `ORDER BY`, `GROUP BY` or
  `DISTINCT ON`. On a query with a post-query mask, `SELECT *` in the outermost select list is
  expanded against the schema cache, so the cache needs an entry for each table the star covers.
  Only tables are expanded: a star there over a subquery, a CTE or a table function is refused
  with the same code, also when the query reads no masked column. List the columns. A star in a
  subquery or CTE over tables without a post-query mask stays as it is.
- A post-query mask is applied by result column position, and DuckDB still computes on the raw
  value. So when a select item carries a post-query mask, these forms are refused with the same
  code:
    - the item's alias used anywhere else in the statement (`SELECT ssn AS x, x AS y`,
      `WHERE x = '1'`, `ORDER BY x`, `GROUP BY x`, `(SELECT x)`). DuckDB resolves a select alias
      in every clause. A body of the statement's own `WITH` clause cannot see the alias, so the
      same name may appear there;
    - the item's position in `ORDER BY`, `GROUP BY` (also inside `ROLLUP`, `CUBE` and
      `GROUPING SETS`) or `DISTINCT ON` (`ORDER BY 2`). The position of another item is fine;
    - `SELECT DISTINCT`;
    - `unnest(...)` in the select list, which can add result columns.

    On any query, a `USING` or `NATURAL` join is refused when a side is a table with a
    post-query mask, and the masked column is refused in the condition of any join. If the result
    still does not have the planned shape (column count, or the column name at a masked
    position), the query is refused before any row is served.
- The subquery cannot hide a post-query masked column that is reached under another name. So on a
  table with a post-query mask these forms are refused with the same code too: the table alias
  used as a value (`SELECT c FROM customers c`, `to_json(c)`, `(c).name`), the table behind its
  schema (`main.customers`), dotted field access into the masked column, a subquery alias that
  reuses the alias of the outer table for another table, and an unqualified column that two
  tables in scope both mask (qualify it with the table name). In a subquery, a star that names
  the table of an outer query (`LATERAL (SELECT c.*)`) and a `SELECT *` without `FROM` are
  refused as well: list the columns. As the whole select list of `EXISTS` both are fine, as
  above. The star is refused by its name, so `c.*` is also refused when `c` is a STRUCT column
  of the subquery. When the subquery reads a table with a post-query mask itself, the code is
  `ERR_UNSUPPORTED_SYNTAX`.
- A bare `CURRENT_DATE`, `CURRENT_TIME`, `CURRENT_TIMESTAMP`, `LOCALTIME` or `LOCALTIMESTAMP` is
  a function, but DuckDB binds the word to a column, a select alias or a table of that name
  first. So the bare word is refused with the same code when that name is a post-query masked
  column in scope, the alias of a select item with a post-query mask, or the alias of a table
  with a post-query mask. Write the name in quotes (`"current_date"`) to select the column.
- On a query with a post-query mask these forms are refused as well, on every table of the
  query, masked or not:
    - a column alias list on a table (`customers AS c(a, b)`) and a join alias
      (`(a JOIN b ON ...) AS j`). A reference to a CTE counts as a table here: `q AS x(a, b)` is
      refused, name the columns in the definition instead (`WITH q(a, b) AS (...)`). A column
      alias list on a subquery or a table function is fine;
    - two FROM items under one alias, a star inside an expression (`c.* LIKE ...`) and a
      positional reference (`#3`);
    - the `alias.` prefix (`SELECT ssn AS x, alias.x`). DuckDB reads `alias.x` as the select
      alias `x`, on the raw value. A table called `alias`, by its name or its alias, is refused
      for the same reason: give it another alias;
    - a column reference with a qualifier that is not a table name or alias of a FROM clause in
      scope. Sluice does not guess what DuckDB makes of such a qualifier. This covers a schema
      or catalog in front of the table (`main.customers.id`) and a column in front of a STRUCT
      field (`address.city`). Write `customers.id` or use an alias, and write `(address).city`
      for the field;
    - `SELECT *` in the outermost select list over a join with `USING` or `NATURAL`
      (`ERR_UNSUPPORTED_SYNTAX`, list the columns).
- Where DuckDB binds a name depends on the clause: a join condition does not see the tables
  joined after it, and binds the name to an outer table instead. Sluice does not follow that. On
  a table with a post-query mask it refuses a name that could reach the masked column, whatever
  else the name could mean:
    - in a subquery or CTE, an unqualified name that is the post-query masked column of a table
      in scope, also when a table of the subquery has a column of that name. Qualify it:
      `(SELECT max(t.name) FROM t)`;
    - a column reference whose last name is the alias of the table (the table name when it has
      no alias), also a real column of another table: `o.placed_at` next to
      `customers placed_at`. Give the table another alias.
- On a query with a mask, a few SQL forms that DuckDB reads in another way than PostgreSQL are
  refused with `ERR_UNSUPPORTED_SYNTAX` or `ERR_SYNTAX`. They are listed, with what to write
  instead, in
  [SQL forms refused on filtered or masked tables](row-filters.md#sql-forms-refused-on-filtered-or-masked-tables).
- One winner per column. When several masks claim the same column, ordering is priority
  descending → selector specificity descending → name ascending; the first candidate takes the
  column and the rest are ignored (masks never stack).

Args are validated at load and again at rewrite by the same provider code, so a bad `args` block
fails `sluice policy validate` before it can fail a query.

!!! warning "Not yet implemented"
    The mask types `custom` (CEL expression masks) and `external` (external masking service)
    parse, but compilation fails with "provider not enabled". Do not reference them in a live
    policy directory.

## null

Replaces the value with SQL `NULL`. No `args`.

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: null-ssn }
spec:
  match: { any: [{ resources: { catalogs: ["hr"], schemas: ["main"], tables: ["employees"], columns: ["ssn"] } }] }
  mask: { type: "null" }
```

`078-05-1120` → `NULL`

## constant

| Arg | Required | Constraint |
| --- | -------- | ---------- |
| `value` | yes | any scalar; written into the rewritten SQL as a constant, so the masked column has the type of the value |

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: constant-email }
spec:
  match: { any: [{ resources: { catalogs: ["shop"], schemas: ["main"], tables: ["customers"], columns: ["email"] } }] }
  mask: { type: constant, args: { value: "REDACTED" } }
```

`alice@example.com` → `REDACTED`

## partial

Reveals the first/last characters and masks the middle. `NULL` passes through.

| Arg | Required | Constraint |
| --- | -------- | ---------- |
| `showFirst` | no (default 0) | integer ≥ 0 |
| `showLast` | no (default 0) | integer ≥ 0 |
| `maskChar` | no (default `*`) | exactly one character |

At least one of `showFirst` or `showLast` must be > 0 — a partial mask with both at 0 fails
`sluice policy validate`.

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: partial-email }
spec:
  match: { any: [{ resources: { catalogs: ["shop"], schemas: ["main"], tables: ["customers"], columns: ["email"] } }] }
  mask: { type: partial, args: { showFirst: 1, showLast: 0 } }
```

`alice@example.com` → `a****************`

## hash

One type, two stages. `sha256` (the default) runs in SQL; `hmac_sha256` runs post-query because
the key must never enter the SQL text.

| Arg | Required | Constraint |
| --- | -------- | ---------- |
| `algorithm` | no (default `sha256`) | `sha256` or `hmac_sha256` |
| `saltRef` | for `hmac_sha256` | `secret://` reference; optional salt prefix for `sha256`, the HMAC key for `hmac_sha256` |

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: hmac-email }
spec:
  match: { any: [{ resources: { catalogs: ["shop"], schemas: ["main"], tables: ["customers"], columns: ["email"] } }] }
  mask: { type: hash, args: { algorithm: hmac_sha256, saltRef: secret://env/MASK_HMAC_KEY } }
```

`alice@example.com` → `ff8d9819fc0e12bf…` (64-char hex; with `sha256` and no salt — HMAC output
depends on the key)

## regex

Rewrites matching substrings via `regexp_replace(col, pattern, replacement, 'g')` — all matches,
not just the first.

| Arg | Required | Constraint |
| --- | -------- | ---------- |
| `pattern` | yes | RE2 syntax, at most 512 bytes, compiled at load |
| `replacement` | no | may be empty — deletes the matches |

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: regex-phone }
spec:
  match: { any: [{ resources: { catalogs: ["shop"], schemas: ["main"], tables: ["customers"], columns: ["phone"] } }] }
  mask: { type: regex, args: { pattern: "[0-9]", replacement: "#" } }
```

`+49 170 1234567` → `+## ### #######`

## truncate

Shortens values longer than `length`; shorter values pass through unchanged.

| Arg | Required | Constraint |
| --- | -------- | ---------- |
| `length` | yes | integer ≥ 1 |
| `suffix` | no | appended after truncation |

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: truncate-notes }
spec:
  match: { any: [{ resources: { catalogs: ["shop"], schemas: ["main"], tables: ["customers"], columns: ["notes"] } }] }
  mask: { type: truncate, args: { length: 8, suffix: "…" } }
```

`Prefers weekend deliveries` → `Prefers …`

## fpe

Format-preserving encryption (FF1) of the in-alphabet characters. Characters outside the alphabet
pass through in place, so separators like `-` or `@` survive. Deterministic per key — the same
input always encrypts to the same output, keeping joins consistent. Post-query.

| Arg | Required | Constraint |
| --- | -------- | ---------- |
| `keyRef` | yes | `secret://` reference to the FF1 key |
| `tweak` | no | hex string, at most 16 bytes decoded |
| `alphabet` | no (default `numeric`) | `numeric`, `lower`, or `alphanumeric` |
| `customAlphabet` | no | 2–36 unique characters; overrides `alphabet` |

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: fpe-card }
spec:
  match: { any: [{ resources: { catalogs: ["shop"], schemas: ["main"], tables: ["payments"], columns: ["card_number"] } }] }
  mask: { type: fpe, args: { keyRef: secret://env/FPE_KEY, alphabet: numeric } }
```

`4111-1111-1111-1111` → `7302-5518-0463-9927` (illustrative — the digits depend on the key; the
format is always preserved)

## jitter

Adds deterministic keyed noise to a numeric value: the output is the input multiplied by
`(1 + f)`, where `f` is derived in `[-range, +range]` from an HMAC of the value — so the same
input always jitters the same way. Non-numeric values abort the query — fail-closed, never
plaintext. Post-query.

| Arg | Required | Constraint |
| --- | -------- | ---------- |
| `range` | yes | float strictly between 0 and 1 (relative noise, e.g. `0.05` = ±5%) |
| `seed` | no | raw string or `secret://` reference keying the noise |

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: jitter-salary }
spec:
  match: { any: [{ resources: { catalogs: ["hr"], schemas: ["main"], tables: ["employees"], columns: ["salary"] } }] }
  mask: { type: jitter, args: { range: 0.05, seed: secret://env/JITTER_SEED } }
```

`72000` → `70311` (illustrative — within ±5%, identical on every re-query)

## fake

Replaces the value with a plausible stand-in picked by a keyed hash of the input — the same value
always maps to the same fake, so joins and GROUP BY stay consistent. Post-query.

| Arg | Required | Constraint |
| --- | -------- | ---------- |
| `fakeType` | yes | `name`, `first_name`, `last_name`, `email`, `phone`, `company`, `city`, `country`, `uuid` |
| `seed` | no | raw string or `secret://` reference keying the mapping |

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: fake-name }
spec:
  match: { any: [{ resources: { catalogs: ["shop"], schemas: ["main"], tables: ["customers"], columns: ["full_name"] } }] }
  mask: { type: fake, args: { fakeType: name } }
```

`Grace Hopper` → `Quinn Reyes` (illustrative — deterministic per seed)

## Recipes

A typical PII bundle — partial emails, nulled SSNs, format-preserving card numbers, jittered
salaries — in one file:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: pii-email, priority: 50 }
spec:
  match: { any: [{ resources: { catalogs: ["shop"], schemas: ["main"], tables: ["customers"], columns: ["email"] } }] }
  mask: { type: partial, args: { showFirst: 1, showLast: 0 } }
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: pii-ssn, priority: 50 }
spec:
  match: { any: [{ resources: { catalogs: ["hr"], schemas: ["main"], tables: ["employees"], columns: ["ssn"] } }] }
  mask: { type: "null" }
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: pii-card, priority: 50 }
spec:
  match: { any: [{ resources: { catalogs: ["shop"], schemas: ["main"], tables: ["payments"], columns: ["card_number"] } }] }
  mask: { type: fpe, args: { keyRef: secret://env/FPE_KEY } }
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: pii-salary, priority: 50 }
spec:
  match: { any: [{ resources: { catalogs: ["hr"], schemas: ["main"], tables: ["employees"], columns: ["salary"] } }] }
  mask: { type: jitter, args: { range: 0.05 } }
```

For a walkthrough with test suites, see the [PII masking cookbook](../cookbook/pii-masking.md).
