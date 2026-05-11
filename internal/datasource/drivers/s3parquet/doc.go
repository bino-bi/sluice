// SPDX-License-Identifier: AGPL-3.0-or-later

// Package s3parquet attaches one or more Parquet datasets stored on
// S3-compatible object storage (AWS S3, MinIO, Cloudflare R2, …). The
// driver:
//
//   - INSTALL/LOAD DuckDB's httpfs extension on first Attach,
//   - CREATE SECRET (TYPE S3, KEY_ID, SECRET, REGION[, ENDPOINT]) from
//     a credentialsRef (resolved to JSON of the shape
//     {"key_id": "...", "secret": "...", "session_token": "..."}),
//   - ATTACH ':memory:' AS <catalog> so DuckDB exposes a namespace,
//   - CREATE OR REPLACE VIEW <catalog>.main.<sanitized> over every entry
//     in spec.allowedPaths via read_parquet(...).
//
// Empty allowedPaths produces a catalog with no views — the spec is
// valid, but every query against the catalog will fail with "table not
// found", matching the default-deny invariant.
package s3parquet
