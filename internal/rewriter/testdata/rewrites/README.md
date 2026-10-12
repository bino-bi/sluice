<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# Golden rewrite fixtures

Each directory is one scenario: `input.sql` + `identity.yaml` + `policies.yaml`
(+ optional `schema.yaml`, a `catalog.schema.table: [columns]` map that feeds a
static schema cache) → `expected.json`. Regenerate
with `go test ./internal/rewriter -run TestGoldenRewrites -update` and review
every diff.

A table with a column mask is read through a wrapper that lists its columns,
so every fixture with a masked table needs a `schema.yaml` that lists it. A
fixture without one gets `ERR_REWRITE_FAILED` (95). A table with only a row
filter keeps `SELECT *` in its wrapper and needs no entry.

Directories 01–30 are feature-named (masks, limits, CEL). Directories 31–48
complete the concept §4.11 edge-case catalog (25 scenarios); the mapping and
the deliberate deviations:

| §4.11 | Fixture | Note |
|---|---|---|
| 1 | `31-star-expansion-rowfilter` | The star stays. DuckDB expands it over the wrapper. |
| 2 | `32-join-rowfilter-both` | |
| 3 | `33-union-rowfilter-one-branch` | |
| 4 | `34-cte-rowfilter-inside` | |
| 5 | `05-column-mask-null`, `16-column-mask-partial` | pre-existing |
| 6 | `20-mask-substitution-in-where` | pre-existing. `WHERE` compares the masked value the wrapper returns. |
| 7 | `35-mask-aggregate-distinct` | |
| 8 | `36-mask-alias-qualified` | |
| 9 | `37-reject-copy-to` | |
| 10 | — | `INSTALL`/`LOAD` is not PG grammar → parse error; live path rejects with `ERR_SYNTAX` in queryservice (`TestExecute_ParseErrorWithAllow`). A fixture here would record the regex-fallback, not the live posture. |
| 11 | `38-rowfilter-where-false` | |
| 12 | — | `PIVOT` — same as 10. |
| 13 | `39-table-function-passthrough` | Concept says "no policy matches ⇒ pass-through". The query references no table, so no table-scoped allow matches and the result is a default-deny. Table functions next to an allowed table are covered by 49 to 63. |
| 14 | `40-explain-rowfilter` | Refused with `ERR_UNSUPPORTED_SYNTAX`: the plan would print the filter with its bound value. `EXPLAIN` is refused whenever a filter or mask applies. |
| 15 | — | `ATTACH` — same as 10. |
| 16 | `41-mask-scalar-subquery` | |
| 17 | `42-mask-outer-subquery-untouched` | |
| 18 | `43-two-rowfilters-and-combined` | |
| 19 | `09-priority-tiebreaker` | pre-existing |
| 20 | `44-recursive-cte-rowfilter` | |
| 21 | `45-mask-in-expression` | |
| 22 | `46-mask-in-case` | |
| 23/24 | — | No per-statement `SET` safelist exists; the executor's `SET lock_configuration = true` hardening blocks *all* user `SET` at execution — strictly tighter than the concept's safelist (`internal/executor/harden.go`, `executor_test.go`). |
| 25 | `47-cross-catalog-both-wrapped`, `48-cross-catalog-reject-cel` | |

Directories 49 to 65 cover the function gate. They use the hello-sluice
policies (allow on `shop.main.customers` and `orders`, tenant row filter,
`email` mask). Policy allows each query because it references an allowed
table; the rewriter must refuse it with `ACL_REJECTED`.

| Fixture | Note |
|---|---|
| `49` to `57` | Table functions in FROM: `query_table`, `duckdb_databases`, `duckdb_settings`, `read_csv`, `read_parquet`, `sqlite_scan`, `glob`, `query`. |
| `58`, `59` | State-reading scalar functions: `getenv`, `current_setting`. |
| `60-reject-columns-expression` | `COLUMNS('email')` picks columns by a pattern, which the post-query mask plan cannot follow. |
| `61-reject-tablesample` | No rewriter pass wraps a `TABLESAMPLE` item, so filter and mask would be skipped. |
| `62-reject-table-function-column-arg` | Allowlisted `unnest` with a column reference in its argument. A post-query masked column would reach the result under a name the Go-side mask cannot trace. |
| `63-table-function-constant-args` | Positive case: `generate_series(1, 2)` next to a filtered table. |
| `64-deny-table-in-values` | Table walker: a table read inside a `VALUES` list is visible to an explicit deny. |
| `65-reject-file-path-table` | A quoted URL as table name. DuckDB would open it as a file and skip filter and mask, so table names with path characters are refused. |

Directories 66 to 82 use the same policies (80 uses the policy of 23, 81 and 82 their own) and cover where the rewrite
passes must reach.

| Fixture | Note |
|---|---|
| `66` to `69` | Row filter on a table that is only read by a subquery in an expression: select list, `WHERE`, `JOIN ... ON`, `HAVING`, `LIMIT`, `VALUES`. |
| `70-mask-values-list` | A masked column inside a `VALUES` row. |
| `71-mask-star-in-subquery` | `SELECT *` in a FROM subquery stays. The wrapper below it returns the masked columns. |
| `72-star-over-subquery-and-table` | An outer star over a subquery and a table stays, because no table has a post-query mask. |
| `73` to `75` | Table alias as a value, column alias list and join alias are served. They read the wrapper, so they only see masked values. |
| `76-identifier-case-folded` | Quoted names in another letter case still get filter and mask. DuckDB resolves names without regard to case. |
| `77-explain-non-select-rejected` | `EXPLAIN ANALYZE` runs its body, so only a `SELECT` body is accepted. |
| `78-star-outer-using-join` | A star over a `FULL` join with `USING` is served. It is not expanded, so DuckDB merges the join key itself. |
| `79-star-keeps-unprotected-items` | A star over a CTE reference, a masked table, a function and a subquery stays as it is. |
| `80-postquery-mask-nested-rejected` | A post-query masked column inside a nested `SELECT` would reach the result under a name the Go-side mask cannot trace. |
| `81-mask-ambiguous-column` | Both tables mask the `USING` column, so the join compares masked values. |
| `82-mask-struct-field` | Dotted field access into a null-masked STRUCT column (`p.info.ssn`) reads NULL. |

Directories 83 to 85 cover names and positions that DuckDB reads in another way than the rewriter planned. 83 and 84 use the policy of 21.

| Fixture | Note |
|---|---|
| `83-postquery-mask-alias-ref-rejected` | DuckDB resolves the select alias of a post-query masked item in `WHERE` and `ORDER BY`, on the raw value. |
| `84-postquery-mask-position-rejected` | `ORDER BY 2` sorts by the raw value of the post-query masked item at position 2. |
| `85-wrapper-alias-keyword-refused` | The wrapper of an unaliased filtered table is named after it. DuckDB reads a bare `semi` as a join keyword, so the table needs an alias. |

Directories 86 to 88 cover the DuckDB parse check. pg_query deparses for PostgreSQL, and DuckDB runs the deparsed text, so a statement whose deparse DuckDB reads in another way is refused. They use the policy of 85 on `orders` alone.

| Fixture | Note |
|---|---|
| `86-interval-week-refused` | pg_query has no `WEEK` interval unit and reads `INTERVAL '1' WEEK` as `'1'::interval AS week`. DuckDB reads one week. |
| `87-isnull-parens-refused` | The deparse drops the parentheses of `(a OR b) IS NULL`, and DuckDB reads `a OR (b IS NULL)`. |
| `88-duckdb-unparseable-refused` | pg_query reads `0x1f` as 31. DuckDB cannot parse `0x1f + 1`, so the statement gets `ERR_SYNTAX`. |

Directories 89 to 92 cover forms the rewriter normalizes for DuckDB before the parse check, so they are served. They use the policy of 86. Without the normalizer, 89 to 91 are refused and 92 is served with lower-case names.

| Fixture | Note |
|---|---|
| `89-like-escape-normalized` | `LIKE ... ESCAPE` and `NOT ILIKE ... ESCAPE` become the 3-argument `like_escape` functions DuckDB reads them as. |
| `90-interval-literal-normalized` | `INTERVAL '30' DAY` becomes the `to_days` call DuckDB expands it to, also in a `RANGE` window frame. The deparsed `'30'::interval day` is 30 seconds in DuckDB. |
| `91-float-json-cast-normalized` | A bare `FLOAT` is `REAL` in DuckDB and `float8` in pg_query. `json` and `bit` lose the `pg_catalog` schema DuckDB does not have. |
| `92-alias-case-restored` | Alias names keep the caller's spelling, because DuckDB keeps it in result column names and struct keys. References stay folded. |

Directories 93 to 100 cover the mask projection in the wrapper. 93 uses the policy of 17, 95 the policy of 06, 96, 97 and 99 the policy of 63, and 98 the policy of 21.

| Fixture | Note |
|---|---|
| `93-cte-and-outer-masked-once` | The table is read in a CTE body and outside it. Each read gets its own wrapper, and the CTE reference is not masked again, also when the CTE has the table's name. |
| `94-rowfilter-not-equals-on-masked-column` | The filter column is also masked. The filter reads the raw value inside the wrapper, the query reads the masked value. Filter params come before mask params. |
| `95-mask-without-schema-fails-closed` | No `schema.yaml`: the wrapper cannot list the columns, so the query gets `ERR_REWRITE_FAILED`. |
| `96-from-item-named-like-masked-column` | A FROM item named like a masked column (`orders email`) is served. |
| `97-output-alias-named-like-table` | An output alias named like the table alias (`count(*) AS c ... HAVING c > 0`) is served. |
| `98-postquery-mask-whole-row-rejected` | The wrapper passes a post-query masked column through raw, so the whole row (`to_json(e)`) stays refused. |
| `99-reserved-name-refused` | The query may not use the wrapper's inner alias `sluice_src`. |
| `100-column-only-mask-projects-every-table` | A mask rule without tables applies to every table. Each one gets the column list wrapper, also a table without the masked column, so a column added at the source later is not read. |

Directories 101 to 108 cover DuckDB forms that reach a masked column without naming it. 101 uses the policy of 60, 102 to 104 the policy of 73, and 105 to 108 the policy of 98.

| Fixture | Note |
|---|---|
| `101-reject-columns-regex` | `COLUMNS('em.*')` picks columns by a regular expression. The function gate rejects every `COLUMNS` call. |
| `102-mask-whole-row-alias` | The table alias as a value (`SELECT c`) is the row of the wrapper, so the struct holds the mask output. |
| `103-mask-unnest-whole-row` | `unnest(c)` expands the row of the wrapper into its columns. |
| `104-mask-star-in-function` | A star inside a function call (`to_json(c.*)`) reads the wrapper. DuckDB refuses `to_json(c.*)` today. |
| `105-postquery-mask-unnest-whole-row-rejected` | The wrapper passes a post-query masked column through raw, so `unnest(e)` stays refused. |
| `106-postquery-mask-star-in-function-rejected` | So does a star inside a function call (`to_json(e.*)`). |
| `107-postquery-mask-outer-star-rejected` | `LATERAL (SELECT e.*)` names a table of the outer SELECT. DuckDB does not expand such a star today, and the rewriter refuses it on its own. |
| `108-postquery-mask-local-star-served` | A star over a subquery of its own SELECT is served, also when the subquery has the name of the outer masked table. |
