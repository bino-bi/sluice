// SPDX-License-Identifier: AGPL-3.0-or-later

package identity

import (
	"errors"
	"fmt"
	"strings"
)

// ErrClaimNotFound indicates the requested JSONPath did not resolve to
// any value in the claim tree. Callers that have an optional claim
// should tolerate this error.
var ErrClaimNotFound = errors.New("identity: claim not found")

// ExtractClaim walks a JSONPath-lite expression against the decoded
// claim map. Supported grammar:
//
//   - Leading "$" is optional; a bare key is treated as "$.key".
//   - Dot-separated segments: "$.realm_access.roles".
//   - Array indexing: "$.groups[0]" pulls the first element.
//   - No wildcards, no filters, no recursion — keeps CEL out of the mix.
//
// When a segment descends into a nested map but encounters a non-map,
// or when an index is out of range, ErrClaimNotFound is returned.
func ExtractClaim(claims map[string]any, path string) (any, error) {
	segments, err := parseClaimPath(path)
	if err != nil {
		return nil, err
	}
	var cursor any = claims
	for _, seg := range segments {
		next, ok := stepClaim(cursor, seg)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrClaimNotFound, path)
		}
		cursor = next
	}
	return cursor, nil
}

// ExtractString resolves path and converts the result to a string. A
// non-string result returns an error so policy matchers don't silently
// compare strings against numbers.
func ExtractString(claims map[string]any, path string) (string, error) {
	v, err := ExtractClaim(claims, path)
	if err != nil {
		return "", err
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("identity: claim %q is not a string (got %T)", path, v)
	}
	return s, nil
}

// ExtractStringList resolves path and returns a []string. Accepts three
// representations an IdP might emit:
//
//   - []any of strings → converted element-wise.
//   - []string → returned (cloned) directly.
//   - string → comma-separated list ("editor,viewer") is split and
//     whitespace-trimmed. Single value comes back as []string{value}.
//
// Any other shape returns an error. Missing path returns an empty slice
// and ErrClaimNotFound so callers can distinguish "absent" from "empty".
func ExtractStringList(claims map[string]any, path string) ([]string, error) {
	v, err := ExtractClaim(claims, path)
	if err != nil {
		return nil, err
	}
	switch raw := v.(type) {
	case []string:
		out := make([]string, len(raw))
		copy(out, raw)
		return out, nil
	case []any:
		out := make([]string, 0, len(raw))
		for i, item := range raw {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("identity: claim %q element %d is not a string (got %T)", path, i, item)
			}
			out = append(out, s)
		}
		return out, nil
	case string:
		if raw == "" {
			return nil, nil
		}
		parts := strings.Split(raw, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				out = append(out, t)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("identity: claim %q is not a list (got %T)", path, v)
	}
}

// parseClaimPath tokenises a path into key / index segments. Returns an
// error on malformed input (unbalanced brackets, empty segment).
func parseClaimPath(path string) ([]claimSeg, error) {
	s := strings.TrimSpace(path)
	if s == "" {
		return nil, errors.New("identity: empty claim path")
	}
	s = strings.TrimPrefix(s, "$")
	s = strings.TrimPrefix(s, ".")
	if s == "" {
		return nil, errors.New("identity: claim path resolves to root")
	}
	var segs []claimSeg
	for len(s) > 0 {
		// A segment is a bare key optionally followed by [N] index suffixes.
		key, rest, err := readKey(s)
		if err != nil {
			return nil, err
		}
		segs = append(segs, claimSeg{key: key})
		for strings.HasPrefix(rest, "[") {
			idx, tail, err := readIndex(rest)
			if err != nil {
				return nil, err
			}
			segs = append(segs, claimSeg{isIndex: true, index: idx})
			rest = tail
		}
		switch {
		case rest == "":
			return segs, nil
		case strings.HasPrefix(rest, "."):
			s = rest[1:]
		default:
			return nil, fmt.Errorf("identity: unexpected claim path char %q", rest[:1])
		}
	}
	return segs, nil
}

type claimSeg struct {
	key     string
	isIndex bool
	index   int
}

func readKey(s string) (string, string, error) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '.' || c == '[' {
			if i == 0 {
				return "", "", errors.New("identity: empty claim path segment")
			}
			return s[:i], s[i:], nil
		}
	}
	return s, "", nil
}

func readIndex(s string) (int, string, error) {
	end := strings.IndexByte(s, ']')
	if end < 0 {
		return 0, "", errors.New("identity: unclosed [ in claim path")
	}
	body := s[1:end]
	if body == "" {
		return 0, "", errors.New("identity: empty [] index in claim path")
	}
	var idx int
	for _, c := range body {
		if c < '0' || c > '9' {
			return 0, "", fmt.Errorf("identity: non-numeric claim index %q", body)
		}
		idx = idx*10 + int(c-'0')
	}
	return idx, s[end+1:], nil
}

func stepClaim(cursor any, seg claimSeg) (any, bool) {
	if seg.isIndex {
		switch arr := cursor.(type) {
		case []any:
			if seg.index < 0 || seg.index >= len(arr) {
				return nil, false
			}
			return arr[seg.index], true
		case []string:
			if seg.index < 0 || seg.index >= len(arr) {
				return nil, false
			}
			return arr[seg.index], true
		default:
			return nil, false
		}
	}
	m, ok := cursor.(map[string]any)
	if !ok {
		return nil, false
	}
	v, present := m[seg.key]
	if !present {
		return nil, false
	}
	return v, true
}
