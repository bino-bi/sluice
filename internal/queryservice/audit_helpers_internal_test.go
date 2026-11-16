// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestClientMetaFor_NilAndEmpty(t *testing.T) {
	if got := clientMetaFor(nil); got != nil {
		t.Fatalf("nil map: got %v want nil", got)
	}
	if got := clientMetaFor(map[string]string{}); got != nil {
		t.Fatalf("empty map: got %v want nil", got)
	}
	if got := clientMetaFor(map[string]string{"": "v"}); got != nil {
		t.Fatalf("empty-key-only map: got %v want nil", got)
	}
}

func TestClientMetaFor_KeepsFirstSortedEntries(t *testing.T) {
	meta := make(map[string]string, maxClientMetaEntries+1)
	for i := range maxClientMetaEntries + 1 {
		meta[fmt.Sprintf("key-%02d", i)] = "v"
	}
	got := clientMetaFor(meta)
	if len(got) != maxClientMetaEntries {
		t.Fatalf("len = %d want %d", len(got), maxClientMetaEntries)
	}
	// Sorted selection: the lexicographically last key must be the one
	// dropped, regardless of map iteration order.
	if _, ok := got[fmt.Sprintf("key-%02d", maxClientMetaEntries)]; ok {
		t.Fatal("lexicographically last key survived; selection not sorted")
	}
	if _, ok := got["key-00"]; !ok {
		t.Fatal("first sorted key missing")
	}
}

func TestClientMetaFor_TruncatesOversizes(t *testing.T) {
	longKey := strings.Repeat("k", maxClientMetaKeyBytes+10)
	longVal := strings.Repeat("v", maxClientMetaValueBytes+10)
	got := clientMetaFor(map[string]string{longKey: longVal})
	wantKey := longKey[:maxClientMetaKeyBytes]
	v, ok := got[wantKey]
	if !ok {
		t.Fatalf("truncated key missing; got %v", got)
	}
	if len(v) != maxClientMetaValueBytes {
		t.Fatalf("value len = %d want %d", len(v), maxClientMetaValueBytes)
	}
}

// A cut inside a multi-byte rune would leave invalid UTF-8 in the record.
func TestCutUTF8_KeepsRunes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 5, "abc"},
		{"abc", 2, "ab"},
		{"aü", 2, "a"},
		{"aü", 3, "aü"},
		{"€x", 2, ""},
	} {
		if got := cutUTF8(tc.in, tc.n); got != tc.want {
			t.Errorf("cutUTF8(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
	got := clientMetaFor(map[string]string{"k": strings.Repeat("a", maxClientMetaValueBytes-1) + "ü"})
	if v := got["k"]; !utf8.ValidString(v) || len(v) != maxClientMetaValueBytes-1 {
		t.Errorf("value = %q, want it cut before the rune", v)
	}
}
