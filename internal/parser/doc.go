// SPDX-License-Identifier: AGPL-3.0-or-later

// Package parser turns SQL strings into a typed AST, computes fingerprints,
// detects multi-statement input, and re-emits rewritten ASTs as SQL.
//
// The only real backend is pg_query_go (internal/pgquery, cgo, PostgreSQL
// grammar). The `pure_parser` build tag selects a stub that fails every
// call. Policy reads the AST through the interfaces here, but the rewriter
// unwraps Raw() and works on pg_query nodes, so it is bound to pg_query.
//
// A regex-based fallback extracts table references from queries the backend
// cannot parse. It is best effort and only orders error codes: such a query
// never runs.
package parser
