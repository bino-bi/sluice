// SPDX-License-Identifier: AGPL-3.0-or-later

package schema

import (
	"context"
	"database/sql"
	"fmt"

	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

// DataSourceResolver is the minimum interface the schema loader needs
// from the data-source registry. Decoupling lets tests swap in fakes
// without pulling in the full registry.
type DataSourceResolver interface {
	// Lookup returns the DataSource for the given catalog name, or a
	// non-nil error when the catalog is unknown or the driver is
	// unhealthy.
	Lookup(catalog string) (pkgds.DataSource, error)
}

// ConnProvider hands out a pooled DuckDB *sql.Conn for a given catalog.
// The cache never owns a connection — it borrows one for the duration
// of a single Load and returns it via the passed Conn's Close.
type ConnProvider interface {
	Conn(ctx context.Context) (*sql.Conn, error)
}

// dataSourceLoader is the production Loader: resolves the DataSource,
// borrows a pooled connection, and calls DataSource.Schema.
type dataSourceLoader struct {
	sources DataSourceResolver
	pool    ConnProvider
}

// NewLoader constructs the standard Loader. nil resolver or pool panic.
func NewLoader(resolver DataSourceResolver, pool ConnProvider) Loader {
	if resolver == nil {
		panic("schema.NewLoader: resolver is required")
	}
	if pool == nil {
		panic("schema.NewLoader: pool is required")
	}
	return &dataSourceLoader{sources: resolver, pool: pool}
}

// Load resolves the catalog, borrows a connection, and returns the
// introspected Schema.
func (l *dataSourceLoader) Load(ctx context.Context, catalog string) (pkgds.Schema, error) {
	ds, err := l.sources.Lookup(catalog)
	if err != nil {
		return pkgds.Schema{}, fmt.Errorf("%w: %w", ErrUnknownCatalog, err)
	}
	conn, err := l.pool.Conn(ctx)
	if err != nil {
		return pkgds.Schema{}, fmt.Errorf("schema: borrow conn: %w", err)
	}
	defer func() { _ = conn.Close() }()
	sch, err := ds.Schema(ctx, conn)
	if err != nil {
		return pkgds.Schema{}, fmt.Errorf("schema: introspect %q: %w", catalog, err)
	}
	if sch.Catalog == "" {
		sch.Catalog = catalog
	}
	return sch, nil
}

// StaticLoader is a Loader backed by an in-memory map. Intended for the
// v2 admin "dry-run" preview and for tests.
type StaticLoader struct {
	// Catalogs maps catalog name → Schema. Missing keys produce
	// ErrUnknownCatalog.
	Catalogs map[string]pkgds.Schema
}

// Load returns the Schema associated with catalog or ErrUnknownCatalog.
func (s *StaticLoader) Load(_ context.Context, catalog string) (pkgds.Schema, error) {
	if s == nil {
		return pkgds.Schema{}, ErrUnknownCatalog
	}
	if sch, ok := s.Catalogs[catalog]; ok {
		return sch, nil
	}
	return pkgds.Schema{}, fmt.Errorf("%w: %q", ErrUnknownCatalog, catalog)
}
