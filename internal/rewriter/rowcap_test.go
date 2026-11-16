// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"testing"

	pg "github.com/pganalyze/pg_query_go/v6"
)

func TestCapPassThrough(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT 1", "SELECT 1\nLIMIT 11"},
		{"SELECT 'ä' AS x;", "SELECT 'ä' AS x\nLIMIT 11;"},
		{"SELECT 1 -- note", "SELECT 1 -- note\nLIMIT 11"},
		{"SELECT 1 /* note */", "SELECT 1 /* note */\nLIMIT 11"},
		{"WITH t AS (SELECT 1 AS a) SELECT a FROM t", "WITH t AS (SELECT 1 AS a) SELECT a FROM t\nLIMIT 11"},
		{"SELECT 1 UNION ALL SELECT 2", "SELECT 1 UNION ALL SELECT 2\nLIMIT 11"},
		{"SELECT 1 LIMIT 5", "SELECT 1 LIMIT 5"},
		{"SELECT 1 LIMIT 500", "SELECT 1 LIMIT 11"},
		{"SELECT 1 LIMIT 1_000 OFFSET 2", "SELECT 1 LIMIT 11 OFFSET 2"},
		{"(SELECT 1 LIMIT 100)", "(SELECT 1 LIMIT 11)"},
		{"SELECT 1 FETCH FIRST 500 ROWS ONLY", "SELECT 1 FETCH FIRST 11 ROWS ONLY"},
		// Left as they are: the iterator still cuts the result.
		{"SELECT 1 OFFSET 3", "SELECT 1 OFFSET 3"},
		{"SELECT 1 LIMIT ALL", "SELECT 1 LIMIT ALL"},
		{"SELECT 1 LIMIT 2 + 600", "SELECT 1 LIMIT 2 + 600"},
		{"SELECT 1 FOR UPDATE", "SELECT 1 FOR UPDATE"},
	}
	for _, tc := range cases {
		tree, err := pg.Parse(tc.in)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.in, err)
		}
		if got := capPassThrough(tc.in, tree, 11); got != tc.want {
			t.Errorf("capPassThrough(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
