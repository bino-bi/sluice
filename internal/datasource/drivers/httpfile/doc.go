// SPDX-License-Identifier: AGPL-3.0-or-later

// Package httpfile attaches remote files served over HTTP(S) as
// read-only views (manifest type "http"). Parquet, CSV, JSON, and Excel
// (xlsx) files are supported; the format is inferred per URL from the
// file extension unless spec.format pins it. The URL list is the
// allowlist — no globs, every file is one explicit URL. The driver:
//
//   - INSTALL/LOAD DuckDB's httpfs extension on first Attach (plus the
//     excel extension when any URL is xlsx),
//   - optionally CREATE SECRET (TYPE HTTP, EXTRA_HTTP_HEADERS …) per URL
//     origin from a headersRef (resolved to a JSON object of header
//     name → value, e.g. {"Authorization": "Bearer …"}), scoped to that
//     origin so headers never travel elsewhere,
//   - ATTACH ':memory:' AS <catalog> so DuckDB exposes a namespace,
//   - CREATE OR REPLACE VIEW <catalog>.main.<sanitized> per URL via the
//     format's reader function.
//
// An empty urls list is rejected at validation time; there is no
// ambient-discovery mode, matching the default-deny invariant.
package httpfile
