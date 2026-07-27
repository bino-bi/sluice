// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

// IntrospectCatalog walks information_schema.columns for the given
// attached catalog and assembles the pkgds.Schema shape shared by the
// view-based drivers (s3, http).
func IntrospectCatalog(ctx context.Context, conn *sql.Conn, catalog string) (pkgds.Schema, error) {
	const q = `
SELECT table_schema, table_name, column_name, data_type, is_nullable, ordinal_position
FROM information_schema.columns
WHERE table_catalog = ?
ORDER BY table_schema, table_name, ordinal_position
`
	rows, err := conn.QueryContext(ctx, q, catalog)
	if err != nil {
		return pkgds.Schema{}, fmt.Errorf("introspect %q: %w", catalog, err)
	}
	defer func() { _ = rows.Close() }()

	sch := pkgds.Schema{Catalog: catalog}
	byNS := map[string]*pkgds.SchemaNS{}
	byTable := map[string]*pkgds.Table{}
	for rows.Next() {
		var (
			nsName, tblName, colName, dataType, isNullable string
			ordinal                                        int32
		)
		if err := rows.Scan(&nsName, &tblName, &colName, &dataType, &isNullable, &ordinal); err != nil {
			return pkgds.Schema{}, fmt.Errorf("introspect %q: scan: %w", catalog, err)
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
		return pkgds.Schema{}, fmt.Errorf("introspect %q: rows: %w", catalog, err)
	}
	return sch, nil
}
