// SPDX-License-Identifier: AGPL-3.0-or-later

// Package parserbackend selects a parser implementation at compile time
// based on build tags. Keeping it separate from internal/parser avoids a
// cycle between the interface package and the pg_query backend.
//
// Default (no tag): the cgo-backed pg_query_go/v6 backend.
// Tag `pure_parser`: a stub that fails every call. The Implemented const
// tells callers whether the active backend is functional; boot paths
// refuse to start on a stub build. A real second backend would also need a
// rewrite of internal/rewriter, which works on pg_query nodes.
package parserbackend
