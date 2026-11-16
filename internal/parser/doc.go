// SPDX-License-Identifier: AGPL-3.0-or-later

// Package parser turns SQL strings into a typed AST, computes fingerprints,
// detects multi-statement input, and re-emits rewritten ASTs as SQL.
//
// The package is backend-agnostic: the default backend is pg_query_go (cgo,
// PostgreSQL-grammar fidelity ~90 % of DuckDB syntax). A pure-Go backend
// (cockroachdb-parser) is selected at compile time via the `pure_parser`
// build tag. Both implement the same Parser interface so the rewriter and
// policy engine never learn which is active.
//
// A regex-based fallback extracts table references from queries the backend
// cannot parse. It is best effort and only orders error codes: such a query
// never runs.
package parser
