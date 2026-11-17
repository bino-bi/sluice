// SPDX-License-Identifier: AGPL-3.0-or-later

package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/bino-bi/sluice/internal/datasource/drivers/common"
	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

// Type matches apitypes.DSMySQL.
const Type = "mysql"

const (
	secretTypeMySQL  = "mysql"
	secretNamePrefix = "sluice_mysql_"
	defaultPort      = 3306
)

// driver captures the parsed connection + credential reference. See
// postgres.driver for the design rationale — the two drivers are
// intentionally kept in lockstep.
type driver struct {
	name     string
	readonly bool

	host     string
	port     int
	database string
	user     string
	sslmode  string

	credentialsRef string
}

// Register installs the factory.
func Register() { pkgds.Register(Type, newDriver) }

func newDriver(_ context.Context, spec pkgds.Spec) (pkgds.DataSource, error) {
	conn, _ := spec.Raw["connection"].(string)
	if conn == "" {
		return nil, errors.New("mysql: spec.connection is required")
	}
	u, err := url.Parse(conn)
	if err != nil {
		return nil, fmt.Errorf("mysql: parse connection: %w", err)
	}
	if u.Scheme != "mysql" && u.Scheme != "mariadb" {
		return nil, fmt.Errorf("mysql: unsupported scheme %q (want mysql:// or mariadb://)", u.Scheme)
	}

	port := defaultPort
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 {
			return nil, fmt.Errorf("mysql: invalid port %q", p)
		}
		port = n
	}
	host := u.Hostname()
	if host == "" {
		return nil, errors.New("mysql: connection host is required")
	}
	database := strings.TrimPrefix(u.Path, "/")
	if database == "" {
		return nil, errors.New("mysql: database name is required in connection URL path")
	}
	user := ""
	if u.User != nil {
		user = u.User.Username()
		if _, ok := u.User.Password(); ok {
			return nil, errors.New("mysql: connection URL holds a password; set it with credentialsRef")
		}
	}
	if user == "" {
		return nil, errors.New("mysql: user is required in connection URL")
	}
	// Only ssl_mode (or its alias sslmode) reaches the DuckDB secret. Any
	// other parameter would be dropped without a word.
	q := u.Query()
	for k := range q {
		if k != "ssl_mode" && k != "sslmode" {
			return nil, fmt.Errorf("mysql: connection URL parameter %q is not supported (only ssl_mode)", k)
		}
	}
	// required: a server or network peer that refuses TLS must not get the
	// password in plain text. Weaker modes only when the URL names them.
	sslmode := q.Get("ssl_mode")
	if sslmode == "" {
		sslmode = q.Get("sslmode")
	}
	if sslmode == "" {
		sslmode = "required"
	}

	credRef, _ := spec.Raw["credentialsRef"].(string)

	return &driver{
		name:           spec.Name,
		readonly:       true,
		host:           host,
		port:           port,
		database:       database,
		user:           user,
		sslmode:        sslmode,
		credentialsRef: credRef,
	}, nil
}

// Name implements pkgds.DataSource.
func (d *driver) Name() string { return d.name }

// Type implements pkgds.DataSource.
func (d *driver) Type() string { return Type }

// Readonly implements pkgds.DataSource.
func (d *driver) Readonly() bool { return d.readonly }

// Attach loads mysql_scanner, creates the secret, and attaches the catalog.
func (d *driver) Attach(ctx context.Context, conn *sql.Conn, opts pkgds.AttachOptions) error {
	catalog := opts.CatalogName
	if catalog == "" {
		catalog = d.name
	}
	if err := common.ValidateIdentifier(catalog); err != nil {
		return fmt.Errorf("mysql: catalog alias: %w", err)
	}

	if err := common.EnsureExtension(ctx, conn, "mysql_scanner"); err != nil {
		return fmt.Errorf("mysql: %w", err)
	}

	password, err := d.resolvePassword(ctx, opts)
	if err != nil {
		return fmt.Errorf("mysql: resolve password: %w", err)
	}

	secretName := secretNamePrefix + d.name
	if err := common.ValidateIdentifier(secretName); err != nil {
		return fmt.Errorf("mysql: secret name: %w", err)
	}
	secretStmt, err := common.BuildCreateSecret(secretName, secretTypeMySQL, []common.SecretArg{
		{Key: "HOST", Value: d.host},
		{Key: "PORT", Value: strconv.Itoa(d.port)},
		{Key: "DATABASE", Value: d.database},
		{Key: "USER", Value: d.user},
		{Key: "PASSWORD", Value: password},
		{Key: "SSL_MODE", Value: d.sslmode},
	})
	if err != nil {
		return fmt.Errorf("mysql: render CREATE SECRET: %w", err)
	}
	if _, err := conn.ExecContext(ctx, secretStmt); err != nil {
		return fmt.Errorf("mysql: CREATE SECRET: %w", err)
	}

	mode := "READ_ONLY"
	if !d.readonly {
		mode = "READ_WRITE"
	}
	attachStmt := fmt.Sprintf(`ATTACH '' AS %s (TYPE MYSQL, SECRET %s, %s)`, catalog, secretName, mode)
	if _, err := conn.ExecContext(ctx, attachStmt); err != nil {
		if common.IsAlreadyAttached(err) {
			return nil
		}
		return fmt.Errorf("mysql: ATTACH %s: %w", catalog, err)
	}
	return nil
}

func (d *driver) resolvePassword(ctx context.Context, opts pkgds.AttachOptions) (string, error) {
	if d.credentialsRef == "" {
		return "", nil
	}
	resolver := opts.SecretResolver
	if resolver == nil {
		return "", errors.New("no SecretResolver supplied but credentialsRef is set")
	}
	b, err := resolver.Resolve(ctx, d.credentialsRef)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\r\n"), nil
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
		return pkgds.Schema{}, fmt.Errorf("mysql: introspect: %w", err)
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
			return pkgds.Schema{}, fmt.Errorf("mysql: scan: %w", err)
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
		return pkgds.Schema{}, fmt.Errorf("mysql: rows: %w", err)
	}
	return sch, nil
}

// HealthCheck runs a cheap query through the attached catalog.
func (d *driver) HealthCheck(ctx context.Context, conn *sql.Conn, opts pkgds.HealthOptions) error {
	q := opts.Query
	if q == "" {
		q = fmt.Sprintf(`SELECT 1 FROM information_schema.schemata WHERE catalog_name = '%s' LIMIT 1`,
			common.EscapeSQLString(d.name))
	}
	row := conn.QueryRowContext(ctx, q)
	var one int
	if err := row.Scan(&one); err != nil {
		return fmt.Errorf("mysql: health: %w", err)
	}
	return nil
}

// Close has nothing to release: extensions, secrets and the catalog
// belong to the DuckDB instance.
func (d *driver) Close() error { return nil }
