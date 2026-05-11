// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import (
	"errors"
	"path"
	"strings"
)

// ErrPathNotAllowed is returned by MatchAllowed when p does not match any
// entry in the allowedPaths whitelist.
var ErrPathNotAllowed = errors.New("s3 path not in allowedPaths")

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

// MatchAllowed reports whether p (a fully-normalised s3:// URI) matches
// any of the glob patterns in allowedPaths. Glob semantics follow
// path.Match, extended so:
//   - "**" matches any number of path segments.
//   - patterns without a scheme are assumed to be "s3://<bucket>/<pattern>".
//
// Empty allowedPaths rejects everything — drivers set the list from
// config, and an empty list means "no S3 access configured".
func MatchAllowed(allowedPaths []string, p string) bool {
	if len(allowedPaths) == 0 || p == "" {
		return false
	}
	for _, pattern := range allowedPaths {
		pat := strings.TrimSpace(pattern)
		if pat == "" {
			continue
		}
		if !strings.Contains(pat, "://") {
			// Bare path — assume s3:// scheme for operator ergonomics.
			pat = "s3://" + strings.TrimLeft(pat, "/")
		}
		if globMatch(pat, p) {
			return true
		}
	}
	return false
}

// globMatch supports "**" for zero-or-more segments and "*" for one
// segment. It is intentionally narrow — DuckDB itself accepts globstar
// syntax in read_parquet(), but our whitelist only needs to decide
// "allowed or not", not enumerate matches.
func globMatch(pattern, s string) bool {
	if pattern == s {
		return true
	}
	// Fast path: "**" at the end means prefix match.
	if prefix, ok := strings.CutSuffix(pattern, "/**"); ok {
		return strings.HasPrefix(s, prefix+"/") || s == prefix
	}
	// "**" in the middle splits into prefix/suffix; prefix is literal
	// and suffix is a glob against the remainder.
	if prefix, suffix, ok := strings.Cut(pattern, "/**/"); ok {
		if !strings.HasPrefix(s, prefix+"/") {
			return false
		}
		rest := strings.TrimPrefix(s, prefix+"/")
		for rest != "" {
			if ok, _ := path.Match(suffix, rest); ok {
				return true
			}
			// Drop one segment and try again.
			cut := strings.Index(rest, "/")
			if cut < 0 {
				break
			}
			rest = rest[cut+1:]
		}
		return false
	}
	// No "**" — fall back to single-segment matching.
	ok, _ := path.Match(pattern, s)
	return ok
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
