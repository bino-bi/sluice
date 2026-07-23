// SPDX-License-Identifier: AGPL-3.0-or-later

package executor_test

import (
	"context"
	"testing"

	"github.com/bino-bi/sluice/internal/executor"
)

// TestExecutorFilterExpressions pins the DuckDB function surface the
// row-filter string operators rely on, using the exact deparsed shapes the
// rewriter emits (function calls with numbered params). starts_with,
// ends_with, and contains compare literally — pattern metacharacters in
// the parameter must not act as wildcards. regexp_matches is a partial
// match unless the pattern is anchored; DuckDB's ~~ (LIKE) keeps raw
// pattern semantics for the Like operator.
func TestExecutorFilterExpressions(t *testing.T) {
	e := newExec(t, executor.Config{})

	cases := []struct {
		name   string
		sql    string
		params []any
		want   bool
	}{
		{
			name:   "starts_with literal match",
			sql:    "SELECT starts_with(v, $1) FROM (SELECT '100% off' AS v)",
			params: []any{"100%"},
			want:   true,
		},
		{
			name:   "starts_with percent is literal",
			sql:    "SELECT starts_with(v, $1) FROM (SELECT '100x off' AS v)",
			params: []any{"100%"},
			want:   false,
		},
		{
			name:   "ends_with underscore is literal",
			sql:    "SELECT ends_with(v, $1) FROM (SELECT 'row a_b' AS v)",
			params: []any{"a_b"},
			want:   true,
		},
		{
			name:   "ends_with underscore no wildcard",
			sql:    "SELECT ends_with(v, $1) FROM (SELECT 'row aXb' AS v)",
			params: []any{"a_b"},
			want:   false,
		},
		{
			name:   "contains literal match",
			sql:    "SELECT contains(v, $1) FROM (SELECT 'x 50%_y' AS v)",
			params: []any{"50%_"},
			want:   true,
		},
		{
			name:   "contains percent no wildcard",
			sql:    "SELECT contains(v, $1) FROM (SELECT 'x 50ab_y' AS v)",
			params: []any{"50%_"},
			want:   false,
		},
		{
			name:   "regexp_matches is partial",
			sql:    "SELECT regexp_matches(v, $1) FROM (SELECT 'hello world' AS v)",
			params: []any{"wor"},
			want:   true,
		},
		{
			name:   "regexp_matches anchored full",
			sql:    "SELECT regexp_matches(v, $1) FROM (SELECT 'hello' AS v)",
			params: []any{"^hello$"},
			want:   true,
		},
		{
			name:   "regexp_matches anchored rejects longer",
			sql:    "SELECT regexp_matches(v, $1) FROM (SELECT 'hello world' AS v)",
			params: []any{"^hello$"},
			want:   false,
		},
		{
			name:   "like keeps raw pattern semantics",
			sql:    "SELECT v ~~ $1 FROM (SELECT '5x off' AS v)",
			params: []any{"5% off"},
			want:   true,
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
			var got bool
			if err := resp.Rows.Scan(&got); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got != tc.want {
				t.Fatalf("result = %v, want %v", got, tc.want)
			}
		})
	}
}
