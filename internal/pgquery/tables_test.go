// SPDX-License-Identifier: AGPL-3.0-or-later

package pgquery_test

import (
	"context"
	"testing"

	"github.com/bino-bi/sluice/internal/parser"
)

// TestExtractTablesNestedExpressions locks the generic descent of the
// table walker: tables referenced only inside expression contexts
// (target-list scalar subqueries, CASE arms, function args, ORDER BY)
// must surface to policy evaluation — hiding one is an ACL bypass.
func TestExtractTablesNestedExpressions(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want []string // table names that must be present
	}{
		{
			name: "scalar subquery in target list",
			sql:  "SELECT (SELECT ssn FROM pg.hr.emp LIMIT 1) AS x FROM pg.public.other",
			want: []string{"emp", "other"},
		},
		{
			name: "exists under case in where",
			sql:  "SELECT dept FROM pg.public.other WHERE CASE WHEN EXISTS (SELECT 1 FROM pg.hr.emp) THEN true ELSE false END",
			want: []string{"emp", "other"},
		},
		{
			name: "subquery in function arg",
			sql:  "SELECT coalesce((SELECT ssn FROM pg.hr.emp LIMIT 1), 'x') FROM pg.public.other",
			want: []string{"emp", "other"},
		},
		{
			name: "subquery in order by",
			sql:  "SELECT dept FROM pg.public.other ORDER BY (SELECT max(ssn) FROM pg.hr.emp)",
			want: []string{"emp", "other"},
		},
		{
			name: "table only in target list",
			sql:  "SELECT (SELECT ssn FROM pg.hr.emp LIMIT 1) AS first_ssn",
			want: []string{"emp"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ast, err := newTestParser(t).Parse(context.Background(), tc.sql)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got := map[string]bool{}
			for _, ref := range ast.Tables() {
				got[ref.Table] = true
			}
			for _, want := range tc.want {
				if !got[want] {
					t.Errorf("table %q not extracted from %q; got %v", want, tc.sql, ast.Tables())
				}
			}
		})
	}
}

// CTE names must still shadow inside generically-walked contexts.
func TestExtractTablesCTENotEmittedFromSubquery(t *testing.T) {
	sql := "WITH x AS (SELECT id FROM pg.public.orders) SELECT (SELECT max(id) FROM x) AS m"
	ast, err := newTestParser(t).Parse(context.Background(), sql)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for _, ref := range ast.Tables() {
		if ref.Table == "x" {
			t.Fatalf("CTE name x leaked as a table ref: %v", ast.Tables())
		}
	}
	found := false
	for _, ref := range ast.Tables() {
		if ref == (parser.TableRef{Catalog: "pg", Schema: "public", Table: "orders"}) {
			found = true
		}
	}
	if !found {
		t.Fatalf("orders not extracted: %v", ast.Tables())
	}
}
