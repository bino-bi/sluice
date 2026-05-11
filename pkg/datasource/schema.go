// SPDX-License-Identifier: Apache-2.0

package datasource

import (
	"fmt"
	"strings"
)

// Schema is the introspected shape of a data source.
type Schema struct {
	Catalog string
	Schemas []SchemaNS
}

// SchemaNS is a namespace (schema) within a catalog.
type SchemaNS struct {
	Name   string
	Tables []Table
}

// Table is a single table within a namespace.
type Table struct {
	Name     string
	Columns  []Column
	Comments string
}

// Column is a single column within a table.
type Column struct {
	Name     string
	SQLType  string
	Nullable bool
	Comment  string
	Position int32
}

// TableKey identifies a fully-qualified table.
type TableKey struct {
	Catalog string
	Schema  string
	Table   string
}

// String renders the key as "catalog.schema.table".
func (t TableKey) String() string {
	return t.Catalog + "." + t.Schema + "." + t.Table
}

// ParseTableKey parses a dotted key. The input must have exactly three
// segments; empty segments and extra dots are rejected.
func ParseTableKey(s string) (TableKey, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return TableKey{}, fmt.Errorf("datasource: table key %q: expected catalog.schema.table (3 segments), got %d", s, len(parts))
	}
	for i, p := range parts {
		if p == "" {
			return TableKey{}, fmt.Errorf("datasource: table key %q: segment %d is empty", s, i+1)
		}
	}
	return TableKey{Catalog: parts[0], Schema: parts[1], Table: parts[2]}, nil
}
