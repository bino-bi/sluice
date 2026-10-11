// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"context"
	"strconv"
	"testing"

	pg "github.com/pganalyze/pg_query_go/v6"
)

type countingChecker map[string]int

func (c countingChecker) ParseTrees(_ context.Context, a, _ string) (string, string, error) {
	c[a]++
	return `{"error":false}`, `{"error":false}`, nil
}

// Past maxSnippetMemo texts, a new text is checked every time and the
// cached ones stay cached.
func TestSnippetMemoBound(t *testing.T) {
	parsed, err := pg.Parse("SELECT 1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ctx := context.Background()
	pc := countingChecker{}
	m := snippetMemo{pc: pc}
	for i := range maxSnippetMemo + 1 {
		if err := m.check(ctx, "SELECT "+strconv.Itoa(i), parsed); err != nil {
			t.Fatalf("check: %v", err)
		}
	}
	for _, src := range []string{"SELECT 0", "SELECT " + strconv.Itoa(maxSnippetMemo)} {
		if err := m.check(ctx, src, parsed); err != nil {
			t.Fatalf("check: %v", err)
		}
	}
	if pc["SELECT 0"] != 1 {
		t.Errorf("cached text checked %d times, want 1", pc["SELECT 0"])
	}
	if n := pc["SELECT "+strconv.Itoa(maxSnippetMemo)]; n != 2 {
		t.Errorf("text past the bound checked %d times, want 2", n)
	}
	if n := m.n.Load(); n != maxSnippetMemo {
		t.Errorf("memo holds %d texts, want %d", n, maxSnippetMemo)
	}
}
