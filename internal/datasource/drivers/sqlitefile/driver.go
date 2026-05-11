// SPDX-License-Identifier: AGPL-3.0-or-later

package sqlitefile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

// Type is the data-source type string matching apitypes.DSSQLite.
const Type = "sqlite"

// driver implements pkgds.DataSource for a local SQLite file.
type driver struct {
	name string
	path string
	// readonly is always true in MVP (write support is v2).
	readonly bool

	mu          sync.Mutex
	extLoaded   bool
	lastProbeOK bool
}

// Register installs the driver factory with pkg/datasource. Call once at
// program start; subsequent calls panic (matches the panic-on-duplicate
// contract documented on pkg/datasource.Register).
func Register() {
	pkgds.Register(Type, newDriver)
}

// newDriver is the factory registered with pkg/datasource.Register.
func newDriver(_ context.Context, spec pkgds.Spec) (pkgds.DataSource, error) {
	path, _ := spec.Raw["path"].(string)
	if path == "" {
		return nil, errors.New("sqlite: spec.path is required")
	}
	// Resolve relative paths against the working directory so behaviour
	// matches the admin's expectation when they drop a file next to the
	// policy directory.
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err == nil {
			path = abs
		}
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("sqlite: stat %q: %w", path, err)
	}
	readonly := true
	if b, ok := spec.Raw["readonly"].(bool); ok && !b {
		// v2 behaviour; MVP always enforces read-only.
		_ = b
	}
	return &driver{
		name:     spec.Name,
		path:     path,
		readonly: readonly,
	}, nil
}

// Name implements pkgds.DataSource.
func (d *driver) Name() string { return d.name }

// Type implements pkgds.DataSource.
func (d *driver) Type() string { return Type }

// Readonly implements pkgds.DataSource.
func (d *driver) Readonly() bool { return d.readonly }

// Attach installs the sqlite_scanner extension (idempotent once per
// connection) and runs ATTACH with the catalog alias. Any error is
// wrapped so callers can distinguish extension problems from ATTACH
// problems.
func (d *driver) Attach(ctx context.Context, conn *sql.Conn, opts pkgds.AttachOptions) error {
	catalog := opts.CatalogName
	if catalog == "" {
		catalog = d.name
	}
	if err := validateIdentifier(catalog); err != nil {
		return fmt.Errorf("sqlite: catalog alias: %w", err)
	}

	d.mu.Lock()
	if !d.extLoaded {
		if _, err := conn.ExecContext(ctx, `INSTALL sqlite`); err != nil {
			d.mu.Unlock()
			return fmt.Errorf("sqlite: INSTALL sqlite: %w", err)
		}
		if _, err := conn.ExecContext(ctx, `LOAD sqlite`); err != nil {
			d.mu.Unlock()
			return fmt.Errorf("sqlite: LOAD sqlite: %w", err)
		}
		d.extLoaded = true
	}
	d.mu.Unlock()

	// Path is taken from driver state (server-controlled config), not a
	// user query parameter — but we still escape the string literal to
	// keep the SQL well-formed.
	stmt := fmt.Sprintf(`ATTACH '%s' AS %s (TYPE sqlite, READ_ONLY)`, escapeSQLString(d.path), catalog)
	if _, err := conn.ExecContext(ctx, stmt); err != nil {
		// DuckDB returns a conflict error when the catalog is already
		// attached on this connection; treat that as success.
		if isAlreadyAttached(err) {
			return nil
		}
		return fmt.Errorf("sqlite: ATTACH %s: %w", catalog, err)
	}
	return nil
}

// Schema returns the information_schema view of the attached catalog.
// DuckDB exposes columns for every attached catalog through
// information_schema.columns, filtered by table_catalog.
func (d *driver) Schema(ctx context.Context, conn *sql.Conn) (pkgds.Schema, error) {
	const q = `
SELECT table_schema, table_name, column_name, data_type, is_nullable, ordinal_position
FROM information_schema.columns
WHERE table_catalog = ?
ORDER BY table_schema, table_name, ordinal_position
`
	rows, err := conn.QueryContext(ctx, q, d.name)
	if err != nil {
		return pkgds.Schema{}, fmt.Errorf("sqlite: introspect: %w", err)
	}
	defer func() { _ = rows.Close() }()

	sch := pkgds.Schema{Catalog: d.name}
	byNamespace := map[string]*pkgds.SchemaNS{}
	byTable := map[string]*pkgds.Table{}

	for rows.Next() {
		var (
			nsName, tblName, colName, dataType, isNullable string
			ordinal                                        int32
		)
		if err := rows.Scan(&nsName, &tblName, &colName, &dataType, &isNullable, &ordinal); err != nil {
			return pkgds.Schema{}, fmt.Errorf("sqlite: scan: %w", err)
		}
		ns := byNamespace[nsName]
		if ns == nil {
			sch.Schemas = append(sch.Schemas, pkgds.SchemaNS{Name: nsName})
			ns = &sch.Schemas[len(sch.Schemas)-1]
			byNamespace[nsName] = ns
		}
		tblKey := nsName + "." + tblName
		tbl := byTable[tblKey]
		if tbl == nil {
			ns.Tables = append(ns.Tables, pkgds.Table{Name: tblName})
			tbl = &ns.Tables[len(ns.Tables)-1]
			byTable[tblKey] = tbl
		}
		tbl.Columns = append(tbl.Columns, pkgds.Column{
			Name:     colName,
			SQLType:  dataType,
			Nullable: strings.EqualFold(isNullable, "YES"),
			Position: ordinal,
		})
	}
	if err := rows.Err(); err != nil {
		return pkgds.Schema{}, fmt.Errorf("sqlite: rows: %w", err)
	}
	return sch, nil
}

// HealthCheck runs a trivial query against the attached catalog. A
// driver-specific query path lets admins confirm the catalog is still
// alive without knowing DuckDB internals.
func (d *driver) HealthCheck(ctx context.Context, conn *sql.Conn, opts pkgds.HealthOptions) error {
	q := opts.Query
	if q == "" {
		// Pick any visible schema — sqlite's default is "main".
		q = fmt.Sprintf("SELECT 1 FROM %s.sqlite_master LIMIT 1", d.name)
	}
	row := conn.QueryRowContext(ctx, q)
	var one int
	if err := row.Scan(&one); err != nil {
		d.mu.Lock()
		d.lastProbeOK = false
		d.mu.Unlock()
		return fmt.Errorf("sqlite: health: %w", err)
	}
	d.mu.Lock()
	d.lastProbeOK = true
	d.mu.Unlock()
	return nil
}

// Close releases driver-level state. SQLite has nothing to close
// outside the connection pool; the method exists so DataSource lifetime
// looks consistent with every other driver.
func (d *driver) Close() error {
	d.mu.Lock()
	d.extLoaded = false
	d.mu.Unlock()
	return nil
}

// validateIdentifier rejects catalog aliases that would inject SQL.
// DuckDB accepts [A-Za-z_][A-Za-z0-9_$]* as an unquoted identifier.
func validateIdentifier(s string) error {
	if s == "" {
		return errors.New("empty")
	}
	for i, r := range s {
		valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' ||
			(i > 0 && ((r >= '0' && r <= '9') || r == '$'))
		if !valid {
			return fmt.Errorf("invalid character %q", r)
		}
	}
	return nil
}

// escapeSQLString doubles single quotes so a path can be safely embedded
// in a SQL string literal. Paths come from server-controlled config so
// this is belt-and-braces; the real defence is "only the server runs
// Attach".
func escapeSQLString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// isAlreadyAttached reports whether err is DuckDB's "already attached"
// error, which is benign when we re-attach after a reload.
func isAlreadyAttached(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already attached") ||
		strings.Contains(msg, "already exists")
}
