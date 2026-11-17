// SPDX-License-Identifier: AGPL-3.0-or-later

package parser

import (
	"fmt"
	"strings"
)

// ParseTableName splits a dotted "catalog.schema.table" name as a client
// writes it in a tool or admin argument. Two parts are catalog.table with
// an empty schema, one part is a bare table. Quoting is not supported.
func ParseTableName(s string) (TableRef, error) {
	parts := strings.Split(s, ".")
	switch len(parts) {
	case 3:
		return TableRef{Catalog: parts[0], Schema: parts[1], Table: parts[2]}, nil
	case 2:
		return TableRef{Catalog: parts[0], Table: parts[1]}, nil
	case 1:
		return TableRef{Table: parts[0]}, nil
	default:
		return TableRef{}, fmt.Errorf("expected catalog[.schema].table, got %q", s)
	}
}
