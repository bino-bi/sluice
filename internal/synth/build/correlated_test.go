// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"database/sql"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// peopleSpec builds crm.people with the group age, income, risk and
// noise_col as distribution. with names only one side of each edge.
func peopleSpec() *apitypes.SyntheticSpec {
	return &apitypes.SyntheticSpec{
		Source:  "crm",
		Seed:    seedRef,
		Privacy: &apitypes.SyntheticPrivacy{MinGroupSize: intp(5)},
		Tables: []apitypes.SyntheticTable{{
			Name: "crm.people",
			Size: apitypes.SyntheticSize{Ratio: f64(0.2)},
			Columns: map[string]apitypes.SyntheticColumn{
				"age":       {Generator: apitypes.GenCorrelated, Args: map[string]any{"with": []any{"income"}}},
				"income":    {Generator: apitypes.GenCorrelated},
				"risk":      {Generator: apitypes.GenCorrelated, Args: map[string]any{"with": []any{"age"}}},
				"noise_col": {Generator: apitypes.GenDistribution},
			},
		}},
	}
}

// spearman is the Spearman correlation of a and b in table, with
// midranks for ties.
func spearman(t *testing.T, db *sql.DB, table, a, b string) float64 {
	t.Helper()
	mid := func(x string) string {
		return "rank() OVER (ORDER BY " + x + ") + (count(*) OVER (PARTITION BY " + x + ") - 1) / 2.0"
	}
	return scalar[float64](t, db, "SELECT corr(ra, rb) FROM (SELECT "+mid(a)+" AS ra, "+mid(b)+" AS rb FROM "+table+
		" WHERE "+a+" IS NOT NULL AND "+b+" IS NOT NULL)")
}

// Test 9.
func TestCorrelatedBuild(t *testing.T) {
	rep, dir := mustRun(t, opts{spec: peopleSpec()})
	db := query(t, Layout{Dir: dir, Name: "crmsynth"}.Snapshot(rep.BuildID))
	for _, p := range [][2]string{{"age", "income"}, {"age", "risk"}, {"income", "risk"}} {
		src := spearman(t, db, "src.crm.people", p[0], p[1])
		syn := spearman(t, db, "syn.crm.people", p[0], p[1])
		if math.Abs(src-syn) > 0.05 {
			t.Errorf("%s, %s: source spearman %.3f, synthetic %.3f", p[0], p[1], src, syn)
		}
	}
	if r := spearman(t, db, "syn.crm.people", "age", "noise_col"); math.Abs(r) > 0.05 {
		t.Errorf("age, noise_col: synthetic spearman %.3f", r)
	}
	// risk has 39 distinct values, so its grid has plateaus and ks is 0.030
	// as with distribution; plan 11 says 0.03.
	for _, c := range []string{"age", "income", "risk"} {
		cr := reportColumn(t, rep, c)
		if cr.Generator != "correlated" || cr.Distance == nil || cr.Distance.Metric != "ks" || cr.Distance.Value > 0.035 {
			t.Errorf("%s report: generator %s, distance %+v", c, cr.Generator, cr.Distance)
		}
	}
	cs := rep.Tables[0].Correlations
	if len(cs) != 1 {
		t.Fatalf("correlations = %+v", cs)
	}
	g := cs[0]
	if !slices.Equal(g.Columns, []string{"age", "income", "risk"}) || len(g.Cells) != 3 || len(g.Synthetic) != 3 || g.MaxDiff > 0.05 {
		t.Errorf("correlation report = %+v", g)
	}
	for _, n := range g.Cells {
		if n != 20 {
			t.Errorf("cells = %v, want 20 each", g.Cells)
		}
	}

	t.Run("date member", func(t *testing.T) {
		spec := peopleSpec()
		spec.Tables[0].Columns["joined"] = apitypes.SyntheticColumn{Generator: apitypes.GenCorrelated, Args: map[string]any{"with": []any{"age"}}}
		_, err := run(t, opts{spec: spec})
		if err == nil || !strings.Contains(err.Error(), "column joined") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("few rows", func(t *testing.T) {
		spec := peopleSpec()
		spec.Tables[0].Size = apitypes.SyntheticSize{Rows: i64(3)}
		rep, _ := mustRun(t, opts{spec: spec})
		cs := rep.Tables[0].Correlations
		if len(cs) != 1 || cs[0].Synthetic != nil || cs[0].MaxDiff != 0 || len(cs[0].Source) != 3 {
			t.Fatalf("correlations = %+v", cs)
		}
		want := "crm.people: correlation of age, income, risk not measured on the snapshot"
		if !slices.Contains(rep.Warnings, want) {
			t.Errorf("warnings = %q, want %q", rep.Warnings, want)
		}
	})
}

// Test 10.
func TestCorrelatedDeterminism(t *testing.T) {
	first, dir1 := mustRun(t, opts{spec: peopleSpec()})
	second, _ := mustRun(t, opts{spec: peopleSpec()})
	if first.ContentHash != second.ContentHash || first.ContentHash == "" {
		t.Fatalf("content hash differs: %s and %s", first.ContentHash, second.ContentHash)
	}
	spec := peopleSpec()
	income := spec.Tables[0].Columns["income"]
	income.Args = map[string]any{"nullRate": 0.1}
	spec.Tables[0].Columns["income"] = income
	nulls, dir2 := mustRun(t, opts{spec: spec})
	a := rowsOf(t, Layout{Dir: dir1, Name: "crmsynth"}.Snapshot(first.BuildID), "crm.people", "age, risk")
	b := rowsOf(t, Layout{Dir: dir2, Name: "crmsynth"}.Snapshot(nulls.BuildID), "crm.people", "age, risk")
	equalRows(t, "income with nulls", a, b)
}
