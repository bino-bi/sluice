// SPDX-License-Identifier: AGPL-3.0-or-later

package identity_test

import (
	"errors"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
)

func TestExtractClaim(t *testing.T) {
	claims := map[string]any{
		"sub":   "alice",
		"email": "alice@example.com",
		"realm_access": map[string]any{
			"roles": []any{"admin", "editor"},
		},
		"nested": map[string]any{
			"deep": map[string]any{"leaf": 42},
		},
	}

	tests := []struct {
		name    string
		path    string
		want    any
		wantErr bool
	}{
		{"top-level", "$.sub", "alice", false},
		{"no-prefix", "email", "alice@example.com", false},
		{"nested", "$.realm_access.roles", []any{"admin", "editor"}, false},
		{"deep", "$.nested.deep.leaf", 42, false},
		{"missing", "$.absent", nil, true},
		{"nested-missing", "$.realm_access.absent", nil, true},
		{"into-non-map", "$.sub.child", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := identity.ExtractClaim(claims, tt.path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want err; got %v", got)
				}
				if !errors.Is(err, identity.ErrClaimNotFound) {
					t.Fatalf("want ErrClaimNotFound; got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if !deepEqual(got, tt.want) {
				t.Fatalf("got %v; want %v", got, tt.want)
			}
		})
	}
}

func TestExtractClaimArrayIndex(t *testing.T) {
	claims := map[string]any{
		"groups": []any{"admins", "users", "beta"},
	}
	got, err := identity.ExtractClaim(claims, "$.groups[1]")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "users" {
		t.Fatalf("got %v; want users", got)
	}

	_, err = identity.ExtractClaim(claims, "$.groups[99]")
	if !errors.Is(err, identity.ErrClaimNotFound) {
		t.Fatalf("want ErrClaimNotFound; got %v", err)
	}
}

func TestExtractStringList(t *testing.T) {
	tests := []struct {
		name   string
		claims map[string]any
		path   string
		want   []string
	}{
		{"any-slice", map[string]any{"g": []any{"a", "b"}}, "g", []string{"a", "b"}},
		{"string-slice", map[string]any{"g": []string{"a", "b"}}, "g", []string{"a", "b"}},
		{"space-not-split", map[string]any{"scope": "read write admin"}, "scope", []string{"read write admin"}},
		{"commas-real", map[string]any{"g": "a,b, c "}, "g", []string{"a", "b", "c"}},
		{"empty-string", map[string]any{"g": ""}, "g", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := identity.ExtractStringList(tt.claims, tt.path)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if !stringSliceEq(got, tt.want) {
				t.Fatalf("got %v; want %v", got, tt.want)
			}
		})
	}
}

func TestExtractStringListTypeMismatch(t *testing.T) {
	claims := map[string]any{"g": 42}
	if _, err := identity.ExtractStringList(claims, "g"); err == nil {
		t.Fatal("want err for non-list claim")
	}
}

func TestParseClaimPathErrors(t *testing.T) {
	tests := []string{
		"",
		"$",
		"[0]",
		"$..",
		"$.key[",
		"$.key[abc]",
		"$.key[]",
	}
	for _, p := range tests {
		if _, err := identity.ExtractClaim(map[string]any{}, p); err == nil {
			t.Fatalf("want err for path %q", p)
		}
	}
}

func stringSliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func deepEqual(a, b any) bool {
	if aa, ok := a.([]any); ok {
		bb, ok := b.([]any)
		if !ok || len(aa) != len(bb) {
			return false
		}
		for i := range aa {
			if !deepEqual(aa[i], bb[i]) {
				return false
			}
		}
		return true
	}
	return a == b
}
