// SPDX-License-Identifier: AGPL-3.0-or-later

package motherduck

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/bino-bi/sluice/internal/datasource/drivers/common"
	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

// Type matches apitypes.DSMotherDuck.
const Type = "motherduck"

// driver attaches to a MotherDuck hosted database. The token is
// resolved on every Attach so rotated tokens propagate on the next
// fresh DuckDB connection.
type driver struct {
	name     string
	readonly bool

	database string
	tokenRef string

	mu        sync.Mutex
	extLoaded map[*sql.Conn]bool
}

// Register installs the factory.
func Register() { pkgds.Register(Type, newDriver) }

func newDriver(_ context.Context, spec pkgds.Spec) (pkgds.DataSource, error) {
	database, _ := spec.Raw["database"].(string)
	if database == "" {
		return nil, errors.New("motherduck: spec.database is required")
	}
	// Strip any "md:" prefix the operator may have copied from DuckDB
	// docs — we add it back in Attach.
	database = strings.TrimPrefix(database, "md:")
	if database == "" {
		return nil, errors.New("motherduck: spec.database cannot be empty after trimming 'md:' prefix")
	}

	tokenRef, _ := spec.Raw["tokenRef"].(string)
	if tokenRef == "" {
		return nil, errors.New("motherduck: spec.tokenRef is required")
	}

	return &driver{
		name:      spec.Name,
		readonly:  true,
		database:  database,
		tokenRef:  tokenRef,
		extLoaded: make(map[*sql.Conn]bool),
	}, nil
}

// Name implements pkgds.DataSource.
func (d *driver) Name() string { return d.name }

// Type implements pkgds.DataSource.
func (d *driver) Type() string { return Type }

// Readonly implements pkgds.DataSource.
func (d *driver) Readonly() bool { return d.readonly }

// Attach installs motherduck, sets the token on this connection, and
// runs ATTACH. Because the SET statement is connection-scoped the
// token is released when the pool recycles the connection.
func (d *driver) Attach(ctx context.Context, conn *sql.Conn, opts pkgds.AttachOptions) error {
	catalog := opts.CatalogName
	if catalog == "" {
		catalog = d.name
	}
	if err := common.ValidateIdentifier(catalog); err != nil {
		return fmt.Errorf("motherduck: catalog alias: %w", err)
	}

	d.mu.Lock()
	if !d.extLoaded[conn] {
		if _, err := conn.ExecContext(ctx, `INSTALL motherduck`); err != nil {
			d.mu.Unlock()
			return fmt.Errorf("motherduck: INSTALL motherduck: %w", err)
		}
		if _, err := conn.ExecContext(ctx, `LOAD motherduck`); err != nil {
			d.mu.Unlock()
			return fmt.Errorf("motherduck: LOAD motherduck: %w", err)
		}
		d.extLoaded[conn] = true
	}
	d.mu.Unlock()

	if opts.SecretResolver == nil {
		return errors.New("motherduck: SecretResolver is required for token resolution")
	}
	raw, err := opts.SecretResolver.Resolve(ctx, d.tokenRef)
	if err != nil {
		return fmt.Errorf("motherduck: resolve token: %w", err)
	}
	token := strings.TrimRight(string(raw), "\r\n")
	if token == "" {
		return errors.New("motherduck: resolved token is empty")
	}

	// SET motherduck_token — connection-local. Not a prepared
	// parameter because DuckDB does not bind SET targets.
	setStmt := fmt.Sprintf(`SET motherduck_token = '%s'`, common.EscapeSQLString(token))
	if _, err := conn.ExecContext(ctx, setStmt); err != nil {
		return fmt.Errorf("motherduck: SET token: %w", err)
	}

	mode := "READ_ONLY"
	if !d.readonly {
		mode = "READ_WRITE"
	}
	// Use a prefix-quoted database name by escaping single quotes; the
	// bare "md:<db>" form has been the long-standing DuckDB convention.
	attachStmt := fmt.Sprintf(`ATTACH 'md:%s' AS %s (TYPE MOTHERDUCK, %s)`,
		common.EscapeSQLString(d.database), catalog, mode)
	if _, err := conn.ExecContext(ctx, attachStmt); err != nil {
		if common.IsAlreadyAttached(err) {
			return nil
		}
		return fmt.Errorf("motherduck: ATTACH %s: %w", catalog, err)
	}
	return nil
}

// Schema introspects information_schema.columns for the attached catalog.
func (d *driver) Schema(ctx context.Context, conn *sql.Conn) (pkgds.Schema, error) {
	const q = `
SELECT table_schema, table_name, column_name, data_type, is_nullable, ordinal_position
FROM information_schema.columns
WHERE table_catalog = ?
ORDER BY table_schema, table_name, ordinal_position
`
	rows, err := conn.QueryContext(ctx, q, d.name)
	if err != nil {
		return pkgds.Schema{}, fmt.Errorf("motherduck: introspect: %w", err)
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
			return pkgds.Schema{}, fmt.Errorf("motherduck: scan: %w", err)
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
		return pkgds.Schema{}, fmt.Errorf("motherduck: rows: %w", err)
	}
	return sch, nil
}

// HealthCheck runs a trivial query against the attached catalog.
func (d *driver) HealthCheck(ctx context.Context, conn *sql.Conn, opts pkgds.HealthOptions) error {
	q := opts.Query
	if q == "" {
		q = fmt.Sprintf(`SELECT 1 FROM information_schema.schemata WHERE catalog_name = '%s' LIMIT 1`,
			common.EscapeSQLString(d.name))
	}
	row := conn.QueryRowContext(ctx, q)
	var one int
	if err := row.Scan(&one); err != nil {
		return fmt.Errorf("motherduck: health: %w", err)
	}
	return nil
}

// Close drops tracked per-connection state.
func (d *driver) Close() error {
	d.mu.Lock()
	d.extLoaded = nil
	d.mu.Unlock()
	return nil
}
