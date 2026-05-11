// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sqlitefile attaches a local SQLite database file to DuckDB via
// the sqlite_scanner extension. The driver:
//
//   - installs and loads sqlite_scanner on first Attach;
//   - ATTACHes the file with the chosen catalog alias;
//   - runs a "SELECT 1 FROM <catalog>.<probe>" health check;
//   - introspects schemas/tables/columns via information_schema.
//
// SQLite needs no credentials or extensions beyond the DuckDB shipped
// sqlite_scanner, so it is the simplest driver to test end-to-end — the
// first driver that lands in the MVP.
package sqlitefile
