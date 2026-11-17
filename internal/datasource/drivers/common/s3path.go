// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import (
	"errors"
	"strings"
)

// NormalizeS3URI rejects obvious malformed inputs and returns a canonical
// s3://bucket/prefix form. Empty inputs are returned as-is (callers
// treat that case as "no user path supplied").
func NormalizeS3URI(uri string) (string, error) {
	u := strings.TrimSpace(uri)
	if u == "" {
		return "", nil
	}
	if !strings.HasPrefix(u, "s3://") {
		return "", errors.New("s3 URI must start with s3://")
	}
	// Collapse duplicate slashes after the scheme so "s3://bucket//a" and
	// "s3://bucket/a" match the same allow rule.
	rest := strings.TrimPrefix(u, "s3://")
	for strings.Contains(rest, "//") {
		rest = strings.ReplaceAll(rest, "//", "/")
	}
	return "s3://" + rest, nil
}

// SanitizeViewName produces an identifier suitable for naming a DuckDB
// view over an allowed path. It replaces every non-identifier character
// with `_`. The caller must still run ValidateIdentifier on the result.
func SanitizeViewName(raw string) string {
	var b strings.Builder
	for i, r := range raw {
		valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' ||
			(i > 0 && ((r >= '0' && r <= '9') || r == '$'))
		if valid {
			_, _ = b.WriteRune(r)
		} else {
			_, _ = b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" || (out[0] >= '0' && out[0] <= '9') {
		out = "v_" + out
	}
	return out
}
