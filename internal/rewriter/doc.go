// SPDX-License-Identifier: AGPL-3.0-or-later

// Package rewriter applies a policy.Decision to a parsed AST, producing
// the final SQL + parameter list the executor sends to DuckDB. The
// pipeline covers statement-kind gating, the post-query mask plan, and a
// wrapper around each filtered or masked table that applies the row
// filter and projects the SQL-masked columns. The rewriter never
// concatenates user-provided values into SQL: row-filter templates and
// mask params always resolve to positional `$N` placeholders.
package rewriter
