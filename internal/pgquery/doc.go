// SPDX-License-Identifier: AGPL-3.0-or-later

// Package pgquery implements the Parser interface on top of
// github.com/pganalyze/pg_query_go/v6. The backend requires cgo. It is the
// only real backend: the rewriter works on its nodes directly.
package pgquery
