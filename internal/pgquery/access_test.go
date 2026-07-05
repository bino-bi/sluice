// SPDX-License-Identifier: AGPL-3.0-or-later

package pgquery_test

import (
	"context"
	"slices"
	"testing"

	"github.com/bino-bi/sluice/internal/parser"
)

func shapeOf(t *testing.T, sql string) parser.QueryShape {
	t.Helper()
	ast, err := newTestParser(t).Parse(context.Background(), sql)
	if err != nil {
		t.Fatalf("parse %q: %v", sql, err)
	}
	return ast.Shape()
}

func TestAccessedColumns_DeepWalk(t *testing.T) {
	s := shapeOf(t, "SELECT id, email FROM pg.hr.employees WHERE dept = 'eng' GROUP BY dept ORDER BY id")
	for _, want := range []string{"id", "email", "dept"} {
		if !slices.Contains(s.AccessedColumns, want) {
			t.Errorf("AccessedColumns %v missing %q", s.AccessedColumns, want)
		}
	}
}

func TestAccessedColumns_Subquery(t *testing.T) {
	s := shapeOf(t, "SELECT id FROM pg.a WHERE tenant IN (SELECT tenant FROM pg.b WHERE secret_flag = true)")
	if !slices.Contains(s.AccessedColumns, "secret_flag") {
		t.Errorf("subquery column not surfaced: %v", s.AccessedColumns)
	}
}

func TestComparisons_ColumnVsLiteral(t *testing.T) {
	s := shapeOf(t, "SELECT id FROM pg.t WHERE country = 'de' AND age >= 18")
	want := []parser.Comparison{
		{Column: "country", Op: "=", Value: "de"},
		{Column: "age", Op: ">=", Value: "18"},
	}
	for _, w := range want {
		if !hasComparison(s.Comparisons, w) {
			t.Errorf("missing comparison %+v in %+v", w, s.Comparisons)
		}
	}
}

func TestComparisons_ReversedOperands(t *testing.T) {
	s := shapeOf(t, "SELECT id FROM pg.t WHERE 18 < age")
	if !hasComparison(s.Comparisons, parser.Comparison{Column: "age", Op: ">", Value: "18"}) {
		t.Errorf("reversed operand not normalised: %+v", s.Comparisons)
	}
}

func TestComparisons_InList(t *testing.T) {
	s := shapeOf(t, "SELECT id FROM pg.t WHERE region IN ('eu', 'us')")
	for _, v := range []string{"eu", "us"} {
		if !hasComparison(s.Comparisons, parser.Comparison{Column: "region", Op: "in", Value: v}) {
			t.Errorf("IN literal %q missing: %+v", v, s.Comparisons)
		}
	}
}

func TestComparisons_IsNull(t *testing.T) {
	s := shapeOf(t, "SELECT id FROM pg.t WHERE deleted_at IS NULL")
	if !hasComparison(s.Comparisons, parser.Comparison{Column: "deleted_at", Op: "isnull"}) {
		t.Errorf("IS NULL not recorded: %+v", s.Comparisons)
	}
}

func TestComparisons_Like(t *testing.T) {
	s := shapeOf(t, "SELECT id FROM pg.t WHERE name LIKE 'A%'")
	if !hasComparison(s.Comparisons, parser.Comparison{Column: "name", Op: "like", Value: "A%"}) {
		t.Errorf("LIKE not recorded: %+v", s.Comparisons)
	}
}

func hasComparison(cs []parser.Comparison, want parser.Comparison) bool {
	return slices.Contains(cs, want)
}
