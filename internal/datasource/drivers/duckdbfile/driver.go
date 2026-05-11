// SPDX-License-Identifier: AGPL-3.0-or-later

package duckdbfile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bino-bi/sluice/internal/datasource/drivers/common"
	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

// Type matches apitypes.DSDuckDBFile.
const Type = "duckdb_file"

// driver attaches a local .duckdb file.
type driver struct {
	name     string
	path     string
	readonly bool

	mu        sync.Mutex
	attached  map[string]bool // per-connection-pointer address tracking not needed; DuckDB dedupes
	lastProbe bool
}

// Register installs the factory with pkg/datasource. Call once at
// program start; subsequent calls panic (contract on pkg/datasource.Register).
func Register() { pkgds.Register(Type, newDriver) }

func newDriver(_ context.Context, spec pkgds.Spec) (pkgds.DataSource, error) {
	path, _ := spec.Raw["path"].(string)
	if path == "" {
		return nil, errors.New("duckdb_file: spec.path is required")
	}
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err == nil {
			path = abs
		}
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("duckdb_file: stat %q: %w", path, err)
	}
	// MVP is always read-only (matches apitypes.AttachReadonly).
	readonly := true
	if b, ok := spec.Raw["readonly"].(bool); ok && !b {
		_ = b // v2 will plumb RW mode through here
	}
	return &driver{
		name:     spec.Name,
		path:     path,
		readonly: readonly,
		attached: make(map[string]bool),
	}, nil
}

// Name implements pkgds.DataSource.
func (d *driver) Name() string { return d.name }

// Type implements pkgds.DataSource.
func (d *driver) Type() string { return Type }

// Readonly implements pkgds.DataSource.
func (d *driver) Readonly() bool { return d.readonly }

// Attach runs a single ATTACH statement. DuckDB is the host engine, so
// no INSTALL/LOAD is needed — ATTACH of a sibling .duckdb file is a
// built-in.
func (d *driver) Attach(ctx context.Context, conn *sql.Conn, opts pkgds.AttachOptions) error {
	catalog := opts.CatalogName
	if catalog == "" {
		catalog = d.name
	}
	if err := common.ValidateIdentifier(catalog); err != nil {
		return fmt.Errorf("duckdb_file: catalog alias: %w", err)
	}
	mode := "READ_ONLY"
	if !d.readonly {
		mode = "READ_WRITE"
	}
	stmt := fmt.Sprintf(`ATTACH '%s' AS %s (TYPE DUCKDB, %s)`,
		common.EscapeSQLString(d.path), catalog, mode)
	if _, err := conn.ExecContext(ctx, stmt); err != nil {
		if common.IsAlreadyAttached(err) {
			return nil
		}
		return fmt.Errorf("duckdb_file: ATTACH %s: %w", catalog, err)
	}
	return nil
}

// Schema introspects information_schema for the attached catalog.
func (d *driver) Schema(ctx context.Context, conn *sql.Conn) (pkgds.Schema, error) {
	const q = `
SELECT table_schema, table_name, column_name, data_type, is_nullable, ordinal_position
FROM information_schema.columns
WHERE table_catalog = ?
ORDER BY table_schema, table_name, ordinal_position
`
	rows, err := conn.QueryContext(ctx, q, d.name)
	if err != nil {
		return pkgds.Schema{}, fmt.Errorf("duckdb_file: introspect: %w", err)
	}
	defer func() { _ = rows.Close() }()

	sch := pkgds.Schema{Catalog: d.name}
	byNS := map[string]*pkgds.SchemaNS{}
	byTable := map[string]*pkgds.Table{}

	for rows.Next() {
		var (
			nsName, tblName, colName, dataType, isNullable string
			ordinal                                        int32
		)
		if err := rows.Scan(&nsName, &tblName, &colName, &dataType, &isNullable, &ordinal); err != nil {
			return pkgds.Schema{}, fmt.Errorf("duckdb_file: scan: %w", err)
		}
		ns := byNS[nsName]
		if ns == nil {
			sch.Schemas = append(sch.Schemas, pkgds.SchemaNS{Name: nsName})
			ns = &sch.Schemas[len(sch.Schemas)-1]
			byNS[nsName] = ns
		}
		key := nsName + "." + tblName
		tbl := byTable[key]
		if tbl == nil {
			ns.Tables = append(ns.Tables, pkgds.Table{Name: tblName})
			tbl = &ns.Tables[len(ns.Tables)-1]
			byTable[key] = tbl
		}
		tbl.Columns = append(tbl.Columns, pkgds.Column{
			Name:     colName,
			SQLType:  dataType,
			Nullable: strings.EqualFold(isNullable, "YES"),
			Position: ordinal,
		})
	}
	if err := rows.Err(); err != nil {
		return pkgds.Schema{}, fmt.Errorf("duckdb_file: rows: %w", err)
	}
	return sch, nil
}

// HealthCheck runs a cheap SELECT against the attached catalog.
func (d *driver) HealthCheck(ctx context.Context, conn *sql.Conn, opts pkgds.HealthOptions) error {
	q := opts.Query
	if q == "" {
		q = fmt.Sprintf(`SELECT 1 FROM information_schema.schemata WHERE catalog_name = '%s' LIMIT 1`,
			common.EscapeSQLString(d.name))
	}
	row := conn.QueryRowContext(ctx, q)
	var one int
	if err := row.Scan(&one); err != nil {
		d.mu.Lock()
		d.lastProbe = false
		d.mu.Unlock()
		return fmt.Errorf("duckdb_file: health: %w", err)
	}
	d.mu.Lock()
	d.lastProbe = true
	d.mu.Unlock()
	return nil
}

// Close releases driver-level state. The attach lives on the DuckDB
// connection pool; releasing it is handled when the pool is torn down.
func (d *driver) Close() error {
	d.mu.Lock()
	d.attached = nil
	d.mu.Unlock()
	return nil
}
