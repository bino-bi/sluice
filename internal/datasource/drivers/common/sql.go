// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import (
	"errors"
	"fmt"
	"strings"
)

// ValidateIdentifier rejects strings that would not be safe to interpolate
// as an unquoted DuckDB identifier (catalog alias, secret name, view name).
// DuckDB accepts `[A-Za-z_][A-Za-z0-9_$]*` unquoted; we enforce the same
// grammar so no driver needs to quote and escape identifiers inline.
func ValidateIdentifier(s string) error {
	if s == "" {
		return errors.New("empty identifier")
	}
	for i, r := range s {
		valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' ||
			(i > 0 && ((r >= '0' && r <= '9') || r == '$'))
		if !valid {
			return fmt.Errorf("invalid character %q in identifier %q", r, s)
		}
	}
	return nil
}

// EscapeSQLString doubles single quotes so the input can be safely embedded
// inside a SQL string literal. Values that flow through here are still
// server-controlled (paths, connection strings, secrets resolved from the
// operator-supplied secret store) — this is defence in depth, not a
// substitute for parameter binding (which CREATE SECRET and ATTACH do not
// support in DuckDB).
func EscapeSQLString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// IsAlreadyAttached reports whether err is DuckDB's "already attached"
// or "already exists" error — which is benign when a reload re-issues
// ATTACH for the same catalog.
func IsAlreadyAttached(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already attached") ||
		strings.Contains(msg, "already exists")
}
