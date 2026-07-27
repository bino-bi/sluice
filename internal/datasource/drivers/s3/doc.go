// SPDX-License-Identifier: AGPL-3.0-or-later

// Package s3 attaches file datasets stored on S3-compatible object
// storage (AWS S3, MinIO, Cloudflare R2, …) as read-only views. Parquet,
// CSV, JSON, and Excel (xlsx) files are supported; the format is
// inferred per path from the file extension unless spec.format pins it.
// The driver:
//
//   - INSTALL/LOAD DuckDB's httpfs extension on first Attach (plus the
//     excel extension when any path is xlsx),
//   - CREATE SECRET (TYPE S3, KEY_ID, SECRET, REGION[, ENDPOINT]) from
//     a credentialsRef (resolved to JSON of the shape
//     {"key_id": "...", "secret": "...", "session_token": "..."}),
//   - ATTACH ':memory:' AS <catalog> so DuckDB exposes a namespace,
//   - CREATE OR REPLACE VIEW <catalog>.main.<sanitized> over every entry
//     in spec.allowedPaths via the format's reader function.
//
// Empty allowedPaths produces a catalog with no views — the spec is
// valid, but every query against the catalog will fail with "table not
// found", matching the default-deny invariant.
package s3
