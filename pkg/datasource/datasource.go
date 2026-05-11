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
//  2. internal/executor calls Attach on each fresh *sql.Conn before
//     locking the DuckDB connection configuration.
//  3. internal/datasource runs HealthCheck on a ticker.
//  4. internal/schema calls Schema lazily, with a TTL cache.
//  5. On shutdown, Close releases driver-level resources.
type DataSource interface {
	// Name returns the catalog name used in queries (matches metadata.name
	// from the YAML).
	Name() string

	// Type returns the driver type (e.g. "postgres", "sqlite").
	Type() string

	// Readonly reports whether the data source is attached read-only.
	Readonly() bool

	// Attach installs the data source into the DuckDB connection. Drivers
	// typically: load required extensions, install per-catalog secrets,
	// and execute ATTACH. Attach may be called concurrently for distinct
	// connections.
	Attach(ctx context.Context, conn *sql.Conn, opts AttachOptions) error

	// Schema returns the visible schemas, tables, and columns. Drivers may
	// cache internally; callers also cache with a TTL.
	Schema(ctx context.Context, conn *sql.Conn) (Schema, error)

	// HealthCheck runs a cheap liveness probe. On failure, the returned
	// error wraps ErrHealthCheck.
	HealthCheck(ctx context.Context, conn *sql.Conn, opts HealthOptions) error

	// Close releases driver-level resources held outside *sql.Conn. Safe
	// to call once.
	Close() error
}

// Reloadable is an optional interface for drivers that can reapply
// configuration (e.g. rotated credentials) without tearing down connections.
// Runtime support lands in v1; declaring the interface here lets drivers
// implement it ahead of time.
type Reloadable interface {
	Reload(ctx context.Context, newSpec Spec) error
}

// WritableDataSource is the v2 write-path extension. Declared here so the
// type system can enforce the MVP read-only contract without a breaking
// change when v2 lands.
type WritableDataSource interface {
	DataSource
	AttachWritable(ctx context.Context, conn *sql.Conn, opts AttachOptions) error
}
