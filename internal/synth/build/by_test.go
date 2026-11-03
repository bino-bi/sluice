// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"bytes"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// R6 and R7.
func TestByColumns(t *testing.T) {
	spec := relSpec(map[string]apitypes.SyntheticColumn{
		"status": {Generator: apitypes.GenCategorical},
		"salary": {Generator: apitypes.GenDistribution, Args: map[string]any{"by": []any{"Status"}}},
		"dept":   {Generator: apitypes.GenCategorical, Args: map[string]any{"by": []any{"status"}}},
	})
	dir := t.TempDir()
	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	rep, _ := mustRun(t, opts{spec: spec, dir: dir, source: relationsPath(t), log: log, tagger: mapTagger{}})
	db := relQuery(t, Layout{Dir: dir, Name: "crmsynth"}.Snapshot(rep.BuildID))

	// R6: the median of each kept group stays close.
	r := scalar[float64](t, db, "SELECT max(salary) - min(salary) FROM src.crm.customers")
	for _, st := range []string{"active", "paused", "closed"} {
		q := "SELECT median(salary) FROM %s.crm.customers WHERE status = '" + st + "'"
		src, syn := scalar[float64](t, db, fmt.Sprintf(q, "src")), scalar[float64](t, db, fmt.Sprintf(q, "syn"))
		if math.Abs(src-syn) > 0.05*r {
			t.Errorf("salary median for %s: source %v, synthetic %v, range %v", st, src, syn, r)
		}
	}
	if n := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.customers WHERE status = 'vip'"); n != 0 {
		t.Errorf("vip appears %d times", n)
	}
	salary := reportColumn(t, rep, "salary")
	want := ByReport{Columns: []string{"status"}, Groups: 3, SpikeGroups: 1}
	if salary.By == nil || !equalBy(*salary.By, want) {
		t.Errorf("salary by = %+v, want %+v", salary.By, want)
	}
	if !slices.Equal(salary.DependsOn, []string{"status"}) || salary.Distance == nil {
		t.Errorf("salary report: depends_on %v, distance %+v", salary.DependsOn, salary.Distance)
	}
	if !slices.Contains(salary.Warnings, "1 by-groups have a spike at a bound") ||
		!slices.Contains(rep.Warnings, "crm.customers.salary: 1 by-groups have a spike at a bound") {
		t.Errorf("warnings: column %v, report %v", salary.Warnings, rep.Warnings)
	}
	if !strings.Contains(logBuf.String(), "by-groups with a spike") {
		t.Error("no spike log line")
	}
	// No group key leaves the builder.
	out := logBuf.String() + strings.Join(rep.Warnings, "\n")
	for _, v := range []string{"active", "paused", "closed", "vip", "39000"} {
		if strings.Contains(out, v) {
			t.Errorf("log or warnings hold %q", v)
		}
	}

	// R7: dept shares per status, and a (status, dept) pair below k never shows.
	const share = "SELECT status || '/' || dept, count(*) / sum(count(*)) OVER (PARTITION BY status) " +
		"FROM %s.crm.customers WHERE status <> 'vip' GROUP BY status, dept"
	src, syn := shares(t, db, fmt.Sprintf(share, "src")), shares(t, db, fmt.Sprintf(share, "syn"))
	for k, v := range src {
		if math.Abs(v-syn[k]) > 0.05 {
			t.Errorf("dept share %s: source %.3f, synthetic %.3f", k, v, syn[k])
		}
	}
	if n := scalar[int64](t, db, "SELECT count(*) FROM src.crm.customers WHERE status = 'paused' AND dept = 'board'"); n != 2 {
		t.Fatalf("fixture: %d paused board rows, want 2", n)
	}
	if n := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.customers WHERE dept = 'board'"); n != 0 {
		t.Errorf("board appears %d times", n)
	}
	if dept := reportColumn(t, rep, "dept"); dept.By == nil || dept.By.Groups != 3 || dept.By.FallbackRows != 0 {
		t.Errorf("dept by = %+v", dept.By)
	}
}

func equalBy(a, b ByReport) bool {
	return slices.Equal(a.Columns, b.Columns) && a.Groups == b.Groups && a.DroppedGroups == b.DroppedGroups &&
		a.FallbackRows == b.FallbackRows && a.SpikeGroups == b.SpikeGroups
}

// A by column that does not reproduce the source values falls back to
// the global profile on most rows.
func TestByFallbackWarning(t *testing.T) {
	spec := relSpec(map[string]apitypes.SyntheticColumn{
		"first_name": {Generator: apitypes.GenFake, Args: map[string]any{"kind": "first_name"}},
		"salary":     {Generator: apitypes.GenDistribution, Args: map[string]any{"by": []any{"first_name"}}},
	})
	rep, _ := mustRun(t, opts{spec: spec, source: relationsPath(t), tagger: mapTagger{}})
	c := reportColumn(t, rep, "salary")
	const w = "most rows use the global profile, the by columns do not match the source groups"
	if c.By == nil || c.By.Groups != 8 || c.By.FallbackRows < 900 || !slices.Contains(c.Warnings, w) {
		t.Errorf("salary by = %+v, warnings %v", c.By, c.Warnings)
	}
}

func TestByErrors(t *testing.T) {
	spec := &apitypes.SyntheticSpec{
		Source:  "crm",
		Seed:    seedRef,
		Privacy: &apitypes.SyntheticPrivacy{MinGroupSize: intp(5)},
		Tables: []apitypes.SyntheticTable{{
			Name: "crm.t",
			Size: apitypes.SyntheticSize{Ratio: f64(0.2)},
			Columns: map[string]apitypes.SyntheticColumn{
				"rare": {Generator: apitypes.GenDistribution, Args: map[string]any{"by": []any{"small"}}},
				"flag": {Generator: apitypes.GenCategorical, Args: map[string]any{"by": []any{"tiny"}}},
				"tiny": {Generator: apitypes.GenBucket, Args: map[string]any{"buckets": 2.0}},
			},
		}},
	}
	_, err := run(t, opts{spec: spec, source: smallFixture(t)})
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{
		"column rare: by column small has type UBIGINT, which cannot be matched",
		"column flag: by column tiny uses bucket, its labels do not match the source values",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not hold %q", err, want)
		}
	}
}

// The Go text of INTEGER, DATE (BC too) and BOOLEAN by values equals the
// DuckDB CAST(... AS VARCHAR) text of the source groups, so every row
// finds its group.
func TestByTypedColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "typed.duckdb")
	c, err := duckdb.NewConnector(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	if _, err := db.Exec(`
CREATE SCHEMA crm;
CREATE TABLE crm.t AS
SELECT
  CAST(i % 3 AS INTEGER) AS n,
  [DATE '2024-01-05', DATE '0999-12-31', DATE '0006-01-02 (BC)'][i % 3 + 1] AS d,
  i % 2 = 0 AS b,
  CAST(i % 3 * 1000 + i % 2 * 100 + i % 7 AS DOUBLE) AS x,
  CAST(i % 3 * 1000 + i % 11 AS DOUBLE) AS y
FROM range(3000) r(i);
`); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	_ = db.Close()
	cat := apitypes.SyntheticColumn{Generator: apitypes.GenCategorical}
	spec := &apitypes.SyntheticSpec{
		Source:  "crm",
		Seed:    seedRef,
		Privacy: &apitypes.SyntheticPrivacy{MinGroupSize: intp(5)},
		Tables: []apitypes.SyntheticTable{{
			Name: "crm.t",
			Size: apitypes.SyntheticSize{Ratio: f64(0.2)},
			Columns: map[string]apitypes.SyntheticColumn{
				"n": cat, "d": cat, "b": cat,
				"x": {Generator: apitypes.GenDistribution, Args: map[string]any{"by": []any{"n", "b"}}},
				"y": {Generator: apitypes.GenDistribution, Args: map[string]any{"by": []any{"d"}}},
			},
		}},
	}
	rep, _ := mustRun(t, opts{spec: spec, source: path, tagger: mapTagger{}})
	for name, groups := range map[string]int{"x": 6, "y": 3} {
		c := reportColumn(t, rep, name)
		if c.By == nil || c.By.Groups != groups || c.By.FallbackRows != 0 {
			t.Errorf("%s by = %+v, want %d groups and no fallback", name, c.By, groups)
		}
	}
}
