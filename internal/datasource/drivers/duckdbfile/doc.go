// SPDX-License-Identifier: AGPL-3.0-or-later

// Package duckdbfile attaches a local DuckDB database file via DuckDB's
// built-in ATTACH. It requires no extension install (DuckDB reads its
// own file format natively) and no credentials.
//
// Use cases: pre-built analytical extracts, DuckLake tables persisted
// to disk, or a shared snapshot file in a read-only pipeline stage.
package duckdbfile
