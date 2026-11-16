// SPDX-License-Identifier: AGPL-3.0-or-later

package parser

// systemObjects are the tables and views of DuckDB's system catalog, by
// schema. Some list the tables and columns of every attached catalog, and
// a policy keyed on table names cannot tell them from user data.
// TestSystemObjectsMatchDuckDB in internal/executor keeps the list in step
// with the system and temp catalogs of the embedded DuckDB.
var systemObjects = map[string]map[string]struct{}{
	"information_schema": nameSet(`character_sets check_constraints columns
		constraint_column_usage constraint_table_usage key_column_usage
		referential_constraints schemata table_constraints tables views`),
	"main": nameSet(`duckdb_columns duckdb_constraints duckdb_databases
		duckdb_indexes duckdb_logs duckdb_schemas duckdb_tables duckdb_types
		duckdb_views pragma_database_list sqlite_master sqlite_schema
		sqlite_temp_master sqlite_temp_schema`),
	"pg_catalog": nameSet(`pg_am pg_attrdef pg_attribute pg_class
		pg_collation pg_constraint pg_database pg_depend pg_description pg_enum
		pg_index pg_indexes pg_namespace pg_prepared_statements pg_proc
		pg_sequence pg_sequences pg_settings pg_tables pg_tablespace pg_type
		pg_views`),
}

// SystemObjects returns the names of systemObjects by schema, sorted.
func SystemObjects() map[string][]string {
	out := make(map[string][]string, len(systemObjects))
	for schema, names := range systemObjects {
		out[schema] = sortedKeys(names)
	}
	return out
}

// IsSystemObject reports whether DuckDB could read t from its system or
// temp catalog. Such a reference is refused before policy:
//
//   - any name in catalog system or temp, written as catalog.schema.t or
//     as the two-part system.t, which DuckDB reads as catalog system;
//   - any name in schema information_schema or pg_catalog;
//   - a name without catalog that is a table or view of system.main or
//     system.pg_catalog, which are on DuckDB's search path.
func IsSystemObject(t TableRef) bool {
	catalog, schema, table := FoldASCII(t.Catalog), FoldASCII(t.Schema), FoldASCII(t.Table)
	switch {
	case catalog == "system" || catalog == "temp":
		return true
	case schema == "information_schema" || schema == "pg_catalog":
		return true
	case catalog != "":
		return false
	case schema == "system" || schema == "temp":
		return true
	case schema != "" && schema != DefaultSchema:
		return false
	}
	_, inMain := systemObjects["main"][table]
	_, inPg := systemObjects["pg_catalog"][table]
	return inMain || inPg
}
