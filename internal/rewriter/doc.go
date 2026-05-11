// SPDX-License-Identifier: AGPL-3.0-or-later

// Package rewriter applies a policy.Decision to a parsed AST, producing
// the final SQL + parameter list the executor sends to DuckDB. The MVP
// pipeline covers statement-kind gating, SELECT-* expansion, wrap-as-
// subquery row-filter injection for the null + constant mask providers,
// and column-mask substitution at every reference site. The rewriter
// never concatenates user-provided values into SQL — row-filter
// templates always resolve to positional `$N` placeholders.
package rewriter
