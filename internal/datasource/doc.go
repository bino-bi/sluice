// SPDX-License-Identifier: AGPL-3.0-or-later

// Package datasource manages the runtime lifecycle of every DataSource
// declared in the policy directory. It wraps the driver registry in
// pkg/datasource, stores instantiated drivers, tracks per-catalog health,
// and exposes an AttachHook that the executor installs on every fresh
// DuckDB connection.
//
// The package is the single source of truth for "which catalogs are
// attached right now?" — the schema cache, the executor, and the admin
// API read state from here.
package datasource
