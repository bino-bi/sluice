// SPDX-License-Identifier: AGPL-3.0-or-later

// Package postgres attaches a PostgreSQL database via DuckDB's
// postgres_scanner extension. Credentials are resolved through
// internal/secrets and injected via CREATE SECRET so the password never
// appears in an ATTACH URL, in DuckDB logs, or in crash dumps.
//
// MVP constraints:
//   - read-only (AttachReadonly) always,
//   - one secret per data source, named `sluice_pg_<catalog>`,
//   - ATTACH ” AS <catalog> (TYPE POSTGRES, SECRET sluice_pg_<catalog>, READ_ONLY).
package postgres
