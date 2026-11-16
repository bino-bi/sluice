// SPDX-License-Identifier: AGPL-3.0-or-later

package parser

import "testing"

func TestIsSystemObject(t *testing.T) {
	for ref, want := range map[TableRef]bool{
		{Table: "duckdb_columns"}:                                   true,
		{Table: "DUCKDB_TABLES"}:                                    true,
		{Table: "pg_class"}:                                         true,
		{Schema: "main", Table: "sqlite_master"}:                    true,
		{Schema: "system", Table: "duckdb_tables"}:                  true,
		{Schema: "pg_catalog", Table: "anything"}:                   true,
		{Schema: "information_schema", Table: "tables"}:             true,
		{Catalog: "shop", Schema: "information_schema", Table: "x"}: true,
		{Catalog: "system", Schema: "main", Table: "x"}:             true,
		{Catalog: "temp", Schema: "main", Table: "x"}:               true,
		{Table: "orders"}:                                           false,
		{Table: "tables"}:                                           false,
		{Schema: "public", Table: "pg_class"}:                       false,
		{Catalog: "shop", Schema: "main", Table: "duckdb_tables"}:   false,
	} {
		if got := IsSystemObject(ref); got != want {
			t.Errorf("IsSystemObject(%+v) = %v, want %v", ref, got, want)
		}
	}
}
