// SPDX-License-Identifier: Apache-2.0

package datasource

import (
	"context"
	"database/sql"
)

// DataSource is the contract every backend driver implements. Drivers live
// in internal/datasource/drivers/* (for AGPL-licensed Sluice-bundled
// drivers) and in third-party packages (for Apache-licensed custom
// drivers). Both speak this interface.
//
// Lifecycle:
//
//  1. cmd/sluice constructs one DataSource per DataSource YAML entry via
//     the factory returned by Lookup(type).
//  2. internal/executor calls Attach once per DuckDB instance, on the
//     first connection, before it locks the configuration.
//  3. internal/datasource runs HealthCheck on a ticker.
//  4. internal/schema calls Schema on a cache miss and keeps the result
//     until a policy reload.
//  5. On shutdown, Close releases driver-level resources.
type DataSource interface {
	// Name returns the catalog name used in queries (matches metadata.name
	// from the YAML).
	Name() string

	// Type returns the driver type (e.g. "postgres", "sqlite").
	Type() string

	// Readonly reports whether the data source is attached read-only.
	Readonly() bool

	// Attach installs the data source into the DuckDB instance through
	// conn. Drivers typically: load required extensions, install
	// per-catalog secrets, and execute ATTACH. Extensions, secrets,
	// settings and the attached catalog belong to the whole instance, not
	// to conn, and stay for the life of the process. Attach runs once,
	// so credentials are read once and a rotation needs a restart.
	Attach(ctx context.Context, conn *sql.Conn, opts AttachOptions) error

	// Schema returns the visible schemas, tables, and columns. Drivers may
	// cache internally; callers also cache until a policy reload.
	Schema(ctx context.Context, conn *sql.Conn) (Schema, error)

	// HealthCheck runs a cheap liveness probe. On failure, the returned
	// error wraps ErrHealthCheck.
	HealthCheck(ctx context.Context, conn *sql.Conn, opts HealthOptions) error

	// Close releases driver-level resources held outside *sql.Conn. Safe
	// to call once.
	Close() error
}

// ExternalPather is an optional interface for drivers whose views read
// remote locations at query time (httpfs-backed s3/http sources).
// ExternalPaths returns the URI prefixes those reads may touch, e.g.
// "s3://bucket/" or "https://host/data/report.csv". The executor renders
// the union of all sources' prefixes into DuckDB's allowed_directories so
// query-time reads succeed while enable_external_access stays disabled.
type ExternalPather interface {
	ExternalPaths() []string
}

// StateReporter is an optional interface for drivers whose data has a
// build state, such as a synthetic source ("not_built", "ready",
// "stale"). The registry copies State into the admin status view.
type StateReporter interface {
	State() string
}
