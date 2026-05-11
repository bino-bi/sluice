// SPDX-License-Identifier: AGPL-3.0-or-later

// Package parserbackend selects a parser implementation at compile time
// based on build tags. Keeping it separate from internal/parser avoids a
// cycle between the interface package and the pg_query backend.
//
// Default (no tag): the cgo-backed pg_query_go/v6 backend.
// Tag `pure_parser`: a stub that will host the cockroachdb-parser backend
// (v2 roadmap).
package parserbackend
