// SPDX-License-Identifier: AGPL-3.0-or-later

package executor_test

import (
	"context"
	"testing"

	"github.com/bino-bi/sluice/internal/executor"
)

// TestExecutorMaskExpressions pins the DuckDB function surface the SQL
// mask providers rely on, using the exact deparsed shapes the rewriter
// emits (lowercase ::varchar casts, GREATEST, numbered params).
func TestExecutorMaskExpressions(t *testing.T) {
	e := newExec(t, executor.Config{})

	cases := []struct {
		name   string
		sql    string
		params []any
		want   string
	}{
		{
			name: "partial",
			sql: "SELECT CASE WHEN v IS NULL THEN NULL ELSE concat(substr(v::varchar, 1, $1), " +
				"repeat($2, GREATEST((length(v::varchar) - $1) - $3, 0)), " +
				"substr(v::varchar, GREATEST((length(v::varchar) - $3) + 1, $1 + 1))) END AS masked " +
				"FROM (SELECT 'alice@example.com' AS v)",
			params: []any{2, "*", 1},
			want:   "al**************m",
		},
		{
			name:   "hash sha256 salted",
			sql:    "SELECT sha256(concat($1, v::varchar)) AS masked FROM (SELECT 'x' AS v)",
			params: []any{"salt"},
			// sha256("saltx")
			want: "c9ef43a03d58f2cb9b0c4b069c179595739f5181a5565c8db5dbb124564ea819",
		},
		{
			name:   "regex replace",
			sql:    "SELECT regexp_replace(v::varchar, $1, $2, 'g') AS masked FROM (SELECT 'a1b2' AS v)",
			params: []any{"[0-9]", "#"},
			want:   "a#b#",
		},
		{
			name: "truncate",
			sql: "SELECT CASE WHEN v IS NULL THEN NULL WHEN length(v::varchar) > $1 " +
				"THEN concat(substr(v::varchar, 1, $1), $2) ELSE v::varchar END AS masked " +
				"FROM (SELECT 'abcdefgh' AS v)",
			params: []any{4, "..."},
			want:   "abcd...",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := e.Execute(context.Background(), executor.Request{SQL: tc.sql, Params: tc.params})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			defer func() { _ = resp.Rows.Close() }()
			if !resp.Rows.Next() {
				t.Fatal("no row")
			}
			var got string
			if err := resp.Rows.Scan(&got); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got != tc.want {
				t.Fatalf("masked = %q, want %q", got, tc.want)
			}
		})
	}
}
