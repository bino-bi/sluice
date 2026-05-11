// SPDX-License-Identifier: Apache-2.0

package datasource

import (
	"context"
	"time"
)

// AttachOptions configures a single Attach call.
type AttachOptions struct {
	// CatalogName overrides the DuckDB catalog alias. When empty, drivers
	// use DataSource.Name().
	CatalogName string

	// SecretResolver resolves secret:// URIs referenced by the spec. Must
	// be non-nil when the spec contains any reference.
	SecretResolver SecretResolver

	// SchemaFilter restricts the visible namespaces. Empty = all.
	SchemaFilter []string

	// TableFilter restricts the visible tables. Empty = all.
	TableFilter []string
}

// HealthOptions configures a single HealthCheck call.
type HealthOptions struct {
	// Query is the SQL executed against the attached catalog. Defaults to
	// "SELECT 1" when empty.
	Query string

	// Timeout bounds the health check. Defaults to 5 seconds when zero.
	Timeout time.Duration
}

// SecretResolver resolves a secret URI (e.g. "secret://env/PG_PASSWORD")
// to its raw bytes. Implementations cache internally; callers may
// re-resolve on reload.
type SecretResolver interface {
	Resolve(ctx context.Context, ref string) ([]byte, error)
}

// Spec is the input to a Factory. It mirrors the subset of
// apitypes.DataSourceSpec a driver needs, stripped of YAML/JSON tags.
type Spec struct {
	Name    string
	Type    string
	Raw     map[string]any
	Secrets SecretResolver
	Filters Filters
}

// Filters are schema/table visibility filters carried on the Spec.
type Filters struct {
	Schemas []string
	Tables  []string
}
