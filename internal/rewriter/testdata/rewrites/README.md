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
| 13 | `39-table-function-passthrough` | Concept says "no policy matches ⇒ pass-through"; under default-deny that is a deny, so the fixture carries an explicit allow and asserts the absence of any rewrite. |
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
