// SPDX-License-Identifier: AGPL-3.0-or-later

// Package pgquery implements the Parser interface on top of
// github.com/pganalyze/pg_query_go/v6. The backend requires cgo; pure-Go
// environments must build with the pure_parser build tag and rely on the
// sibling `cockroach` backend (v2 roadmap).
package pgquery
