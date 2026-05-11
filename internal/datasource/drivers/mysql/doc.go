// SPDX-License-Identifier: AGPL-3.0-or-later

// Package mysql attaches a MySQL or MariaDB database via DuckDB's
// mysql_scanner extension. Credentials are resolved through
// internal/secrets and injected via CREATE SECRET so the password never
// appears in an ATTACH URL, in DuckDB logs, or in crash dumps.
//
// MVP constraints mirror the postgres driver:
//   - read-only always,
//   - one secret per data source, named `sluice_mysql_<catalog>`,
//   - ATTACH ” AS <catalog> (TYPE MYSQL, SECRET sluice_mysql_<catalog>, READ_ONLY).
package mysql
