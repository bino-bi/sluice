<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# Golden rewrite fixtures

Each directory is one scenario: `input.sql` + `identity.yaml` + `policies.yaml`
(+ optional `schema.yaml`, a `catalog.schema.table: [columns]` map that feeds a
static schema cache for `SELECT *` expansion) → `expected.json`. Regenerate
with `go test ./internal/rewriter -run TestGoldenRewrites -update` and review
every diff.

Directories 01–30 are feature-named (masks, limits, CEL). Directories 31–48
complete the concept §4.11 edge-case catalog (25 scenarios); the mapping and
the deliberate deviations:

| §4.11 | Fixture | Note |
|---|---|---|
| 1 | `31-star-expansion-rowfilter` | |
| 2 | `32-join-rowfilter-both` | |
| 3 | `33-union-rowfilter-one-branch` | |
| 4 | `34-cte-rowfilter-inside` | |
| 5 | `05-column-mask-null`, `16-column-mask-partial` | pre-existing |
| 6 | `20-mask-substitution-in-where` | pre-existing |
| 7 | `35-mask-aggregate-distinct` | |
| 8 | `36-mask-alias-qualified` | |
| 9 | `37-reject-copy-to` | |
| 10 | — | `INSTALL`/`LOAD` is not PG grammar → parse error; live path rejects with `ERR_SYNTAX` in queryservice (`TestExecute_ParseErrorWithAllow`). A fixture here would record the regex-fallback, not the live posture. |
| 11 | `38-rowfilter-where-false` | |
| 12 | — | `PIVOT` — same as 10. |
| 13 | `39-table-function-passthrough` | Concept says "no policy matches ⇒ pass-through". The query references no table, so no table-scoped allow matches and the result is a default-deny. Table functions next to an allowed table are covered by 49 to 63. |
| 14 | `40-explain-rowfilter` | |
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
| `60-reject-columns-expression` | `COLUMNS('email')` expands to the raw column after the mask pass. |
| `61-reject-tablesample` | No rewriter pass wraps a `TABLESAMPLE` item, so filter and mask would be skipped. |
| `62-reject-table-function-column-arg` | Allowlisted `unnest` with a column reference in its argument. The mask pass does not walk FROM function arguments. |
| `63-table-function-constant-args` | Positive case: `generate_series(1, 2)` next to a filtered table. |
| `64-deny-table-in-values` | Table walker: a table read inside a `VALUES` list is visible to an explicit deny. |
| `65-reject-file-path-table` | A quoted URL as table name. DuckDB would open it as a file and skip filter and mask, so table names with path characters are refused. |

Directories 66 to 82 use the same policies (80 uses the policy of 23, 82 its own) and cover where the rewrite
passes must reach.

| Fixture | Note |
|---|---|
| `66` to `69` | Row filter on a table that is only read by a subquery in an expression: select list, `WHERE`, `JOIN ... ON`, `HAVING`, `LIMIT`, `VALUES`. |
| `70-mask-values-list` | A masked column inside a `VALUES` row. |
| `71-mask-star-in-subquery` | `SELECT *` in a FROM subquery is expanded and masked there. |
| `72-star-over-subquery-and-table` | An outer star keeps `q.*` for the subquery and lists the table columns in FROM order. |
| `73` to `75` | Forms the mask pass cannot follow are refused: table alias as a value, column alias list, join alias. |
| `76-identifier-case-folded` | Quoted names in another letter case still get filter and mask. DuckDB resolves names without regard to case. |
| `77-explain-non-select-rejected` | `EXPLAIN ANALYZE` runs its body, so only a `SELECT` body is accepted. |
| `78-star-outer-using-join-rejected` | A star over a `RIGHT` or `FULL` join with `USING` cannot be listed column by column without changing the result. |
| `79-star-keeps-unprotected-items` | A star keeps `alias.*` for a CTE reference, a function and a subquery, and names an unaliased subquery. |
| `80-postquery-mask-nested-rejected` | A post-query masked column inside a nested `SELECT` would reach the result under a name the Go-side mask cannot trace. |
| `81-mask-ambiguous-column-rejected` | An unqualified column that two tables of one FROM clause mask is refused instead of served raw. |
| `82-mask-struct-field-rejected` | Dotted field access into a masked STRUCT column (`p.info.ssn`). |
