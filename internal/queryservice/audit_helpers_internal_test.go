// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice

import (
	"fmt"
	"strings"
	"testing"
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
