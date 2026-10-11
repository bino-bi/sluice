<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Column masks

`ColumnMaskPolicy` replaces the values of matched columns before they reach the client. Masks run
at one of two stages:

| Stage | Providers | How |
| ----- | --------- | --- |
| SQL | `null`, `constant`, `partial`, `hash` (sha256), `regex`, `truncate` | the masked expression replaces the column reference in the rewritten query — plaintext never leaves DuckDB |
| Post-query | `hash` (hmac_sha256), `fpe`, `jitter`, `fake` | applied in Go to each result row as it streams, because the transform needs key material or logic SQL cannot express |

Matching rules to know:

- A mask **matches nothing unless the selector sets `resources.columns`**. A clause with only
  tables silently masks no columns.
- `SELECT *` cannot dodge a mask: star expansion (via the schema cache) runs before mask
  substitution, so every expanded column is checked. This includes a star inside a FROM subquery,
  a CTE, or a subquery in an expression.
- A mask follows the column by name, without regard to letter case. Forms that reach the column
  under another name are refused with `ERR_MASK_UNSUPPORTED_CONTEXT` when the table has a mask:
  the table alias used as a value (`SELECT c FROM customers c`, `to_json(c)`, `(c).email`), a
  column alias list (`customers AS c(a, b)`), and an alias on a join
  (`(a JOIN b ON ...) AS j`). Select the columns by their own names instead.
- The same code is returned for other forms that would get around the substitution: the
  schema-qualified table used as a value (`main.customers`), two FROM items under one alias, a
  positional reference (`#3`), dotted field access into a masked STRUCT column (`info.ssn`), and
  an unqualified column that two tables of one FROM clause both mask. Qualify the column with the
  table name in the last case.
- A post-query masked column can only be a bare item of the outermost select list. Anywhere else
  it is refused: inside an expression, in `ORDER BY` or `DISTINCT ON`, and inside a subquery or
  CTE. That includes `SELECT *` in a subquery or CTE over such a table (list the columns you need
  there), except inside `EXISTS (...)`.
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
- A star is only supported as a select item. `c.*` inside an expression is refused on a query
  with masks. An alias in a subquery must not reuse the alias of an outer masked table for
  another table.
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
| `value` | yes | any scalar; bound as a parameter, never spliced into SQL |

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
