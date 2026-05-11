// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import (
	"fmt"
	"sort"
	"strings"
)

// SecretArg is one argument of a DuckDB CREATE SECRET block. Keys are
// DuckDB identifiers (TYPE, KEY_ID, REGION …); values are rendered as
// single-quoted SQL strings. The caller decides which keys are sensitive
// — rendering does not differ between categories, but the caller should
// take care that secret values are resolved via internal/secrets and
// never logged.
type SecretArg struct {
	Key   string
	Value string
}

// BuildCreateSecret renders a DuckDB CREATE OR REPLACE SECRET statement.
// The secret name is validated as an identifier; the secretType is
// rendered verbatim (must be one of DuckDB's secret types — POSTGRES,
// MYSQL, S3, MOTHERDUCK, …); every SecretArg.Value is escaped.
//
// Using CREATE OR REPLACE keeps reloads idempotent without forcing the
// caller to query catalog state — DuckDB silently drops the prior
// secret when the name matches.
func BuildCreateSecret(name, secretType string, args []SecretArg) (string, error) {
	if err := ValidateIdentifier(name); err != nil {
		return "", fmt.Errorf("secret name: %w", err)
	}
	if secretType == "" {
		return "", fmt.Errorf("secret type is required")
	}
	if err := ValidateIdentifier(secretType); err != nil {
		return "", fmt.Errorf("secret type: %w", err)
	}

	var b strings.Builder
	_, _ = b.WriteString("CREATE OR REPLACE SECRET ")
	_, _ = b.WriteString(name)
	_, _ = b.WriteString(" (\n    TYPE ")
	_, _ = b.WriteString(strings.ToUpper(secretType))
	// Order arguments by key for deterministic output — eases golden
	// tests and keeps logs diffable when we accidentally leak them.
	sorted := append([]SecretArg(nil), args...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })
	for _, a := range sorted {
		if err := ValidateIdentifier(a.Key); err != nil {
			return "", fmt.Errorf("secret arg key %q: %w", a.Key, err)
		}
		_, _ = b.WriteString(",\n    ")
		_, _ = b.WriteString(strings.ToUpper(a.Key))
		_, _ = b.WriteString(" '")
		_, _ = b.WriteString(EscapeSQLString(a.Value))
		_, _ = b.WriteString("'")
	}
	_, _ = b.WriteString("\n)")
	return b.String(), nil
}
