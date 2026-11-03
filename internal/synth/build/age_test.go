// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"database/sql"
	"fmt"
	"math"
	"path/filepath"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// lifeFixture writes crm.life: birth has ages 25 to 29, 40 to 44
// and 60 to 64 at 2024-06-30 in shares 0.3, 0.5, 0.2, plus 3 rows of
// age 75 and one of age 90. tod and seen peak at office hours: 70% from
// 9 to 16 o'clock, 20% from 18 to 21, 10% spread over the day.
func lifeFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "people.duckdb")
	c, err := duckdb.NewConnector(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`
CREATE SCHEMA crm;
CREATE TABLE crm.life AS
WITH g AS (
  SELECT i,
    CASE WHEN i = 0 THEN 90 WHEN i BETWEEN 1 AND 3 THEN 75
         WHEN i % 10 < 3 THEN 25 + i % 5 WHEN i % 10 < 8 THEN 40 + i % 5 ELSE 60 + i % 5 END AS age,
    CASE WHEN i % 10 < 7 THEN 9 + i % 8 WHEN i % 10 < 9 THEN 18 + i % 4 ELSE (i * 7) % 24 END AS h
  FROM range(20000) r(i)
)
SELECT
  CAST(DATE '2024-06-30' - to_years(CAST(age AS INTEGER)) - to_days(CAST(i % 360 AS INTEGER)) AS DATE) AS birth,
  make_time(h, (i * 13) % 60, (i * 7) % 60) AS tod,
  DATE '2024-01-01' + CAST(i % 200 AS INTEGER) + make_time(h, (i * 13) % 60, (i * 7) % 60) AS seen
FROM g;
`); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return path
}

func lifeSpec(cols map[string]apitypes.SyntheticColumn) *apitypes.SyntheticSpec {
	return &apitypes.SyntheticSpec{
		Source:  "crm",
		Seed:    seedRef,
		Privacy: &apitypes.SyntheticPrivacy{MinGroupSize: intp(5)},
		Tables:  []apitypes.SyntheticTable{{Name: "crm.life", Size: apitypes.SyntheticSize{Ratio: f64(0.2)}, Columns: cols}},
	}
}

func lifeQuery(t *testing.T, source, snapshot string) *sql.DB {
	t.Helper()
	c, err := duckdb.NewConnector("", nil)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, s := range []string{
		"ATTACH '" + source + "' AS src (READ_ONLY)",
		"ATTACH '" + snapshot + "' AS syn (READ_ONLY)",
		"SET TimeZone = 'UTC'",
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return db
}

func compareShares(t *testing.T, db *sql.DB, what, q string, tol float64) {
	t.Helper()
	src, syn := shares(t, db, fmt.Sprintf(q, "src")), shares(t, db, fmt.Sprintf(q, "syn"))
	for k, v := range src {
		if math.Abs(v-syn[k]) > tol {
			t.Errorf("%s share %s: source %.3f, synthetic %.3f", what, k, v, syn[k])
		}
	}
	for k := range syn {
		if _, ok := src[k]; !ok {
			t.Errorf("%s %s appears only in the synthetic data", what, k)
		}
	}
}

func TestAgeAndTimeOfDay(t *testing.T) {
	src := lifeFixture(t)
	spec := lifeSpec(map[string]apitypes.SyntheticColumn{
		"birth": {Generator: apitypes.GenAge, Args: map[string]any{"asOf": "2024-06-30"}},
		"tod":   {Generator: apitypes.GenTimeOfDay},
		"seen":  {Generator: apitypes.GenTimeOfDay},
	})
	rep, dir := mustRun(t, opts{spec: spec, source: src})
	db := lifeQuery(t, src, Layout{Dir: dir, Name: "crmsynth"}.Snapshot(rep.BuildID))

	const ages = "SELECT CASE WHEN a < 35 THEN '25-29' WHEN a < 50 THEN '40-44' WHEN a < 70 THEN '60-64' ELSE 'old' END, " +
		"count(*) / sum(count(*)) OVER () FROM (SELECT date_sub('year', birth, DATE '2024-06-30') AS a FROM %s.crm.life) GROUP BY 1"
	compareShares(t, db, "age band", ages, 0.03)
	if n := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.life WHERE date_sub('year', birth, DATE '2024-06-30') NOT BETWEEN 25 AND 64"); n != 0 {
		t.Errorf("%d rows with an age below minGroupSize rows", n)
	}
	if c := reportColumn(t, rep, "birth"); c.SuppressedValues != 2 {
		t.Errorf("birth suppressed = %d, want 2", c.SuppressedValues)
	}

	for _, col := range []string{"tod", "seen"} {
		q := "SELECT CAST(hour(" + col + ") AS VARCHAR), count(*) / sum(count(*)) OVER () FROM %s.crm.life GROUP BY 1"
		compareShares(t, db, col+" hour", q, 0.02)
	}
	if n := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.life WHERE CAST(seen AS DATE) NOT BETWEEN DATE '2023-12-25' AND DATE '2024-07-25'"); n != 0 {
		t.Errorf("%d seen dates far outside the source range", n)
	}

	again, _ := mustRun(t, opts{spec: spec, source: src})
	if again.ContentHash != rep.ContentHash {
		t.Errorf("content hash differs: %s and %s", rep.ContentHash, again.ContentHash)
	}
}

// Without asOf the reference is hi' of birth, so the ages stay close to
// the ages at the source max. Both catalogs are measured at the source
// max, so a wrong reference moves the synthetic ages. The bands leave
// room for extremeJitter (2% of 65 years).
func TestAgeDefaultReference(t *testing.T) {
	src := lifeFixture(t)
	spec := lifeSpec(map[string]apitypes.SyntheticColumn{"birth": {Generator: apitypes.GenAge, Args: map[string]any{"granularity": "month"}}})
	rep, dir := mustRun(t, opts{spec: spec, source: src})
	db := lifeQuery(t, src, Layout{Dir: dir, Name: "crmsynth"}.Snapshot(rep.BuildID))
	if n := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.life WHERE day(birth) <> 1"); n != 0 {
		t.Errorf("%d dates not cut to the month", n)
	}
	const mix = "SELECT CASE WHEN a < 10 THEN '0-4' WHEN a < 27 THEN '15-19' WHEN a < 45 THEN '35-39' ELSE 'old' END, count(*) / sum(count(*)) OVER () " +
		"FROM (SELECT date_sub('year', birth, (SELECT max(birth) FROM src.crm.life)) AS a FROM %s.crm.life) GROUP BY 1"
	compareShares(t, db, "age band", mix, 0.05)
	again, _ := mustRun(t, opts{spec: spec, source: src})
	if again.ContentHash != rep.ContentHash {
		t.Errorf("content hash differs: %s and %s", rep.ContentHash, again.ContentHash)
	}
}

func TestAgeAndTimeOfDayNoise(t *testing.T) {
	src := lifeFixture(t)
	spec := lifeSpec(map[string]apitypes.SyntheticColumn{
		"birth": {Generator: apitypes.GenAge, Args: map[string]any{"asOf": "2024-06-30"}},
		"tod":   {Generator: apitypes.GenTimeOfDay},
		"seen": {Generator: apitypes.GenTimeOfDay, Privacy: &apitypes.SyntheticPrivacy{
			Bounds: &apitypes.SyntheticBounds{Min: "2024-01-01", Max: "2024-07-31"}}},
	})
	spec.Privacy.Noise = &apitypes.SyntheticNoise{Epsilon: 10}
	rep, dir := mustRun(t, opts{spec: spec, source: src})
	// nulls plus ages; nulls plus slots; nulls, numbers and slots.
	for col, want := range map[string]int{"birth": 2, "tod": 2, "seen": 3} {
		if c := reportColumn(t, rep, col); c.NoiseMeasurements != want {
			t.Errorf("%s measurements = %d, want %d", col, c.NoiseMeasurements, want)
		}
	}
	if c := reportColumn(t, rep, "tod"); c.Range != "" {
		t.Errorf("tod range = %q, want none", c.Range)
	}
	db := lifeQuery(t, src, Layout{Dir: dir, Name: "crmsynth"}.Snapshot(rep.BuildID))
	if n := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.life WHERE date_sub('year', birth, DATE '2024-06-30') NOT BETWEEN 25 AND 64"); n != 0 {
		t.Errorf("%d rows with a rare age", n)
	}
	q := "SELECT CAST(hour(tod) AS VARCHAR), count(*) / sum(count(*)) OVER () FROM %s.crm.life GROUP BY 1"
	compareShares(t, db, "tod hour", q, 0.03)
}

// clipOutliers cuts the dates, so upper 0.7 drops the latest 30% of
// birth dates: the ages 25 to 29. Under noise and bounds the age
// histogram is cut, and the cut must land on the same end.
func TestAgeClipNoiseBounds(t *testing.T) {
	src := lifeFixture(t)
	spec := lifeSpec(map[string]apitypes.SyntheticColumn{
		"birth": {Generator: apitypes.GenAge, Args: map[string]any{"asOf": "2024-06-30"}, Privacy: &apitypes.SyntheticPrivacy{
			Bounds:       &apitypes.SyntheticBounds{Min: "1930-01-01", Max: "2000-01-01"},
			ClipOutliers: &apitypes.ClipOutliers{Lower: 0, Upper: 0.7}}},
	})
	spec.Privacy.Noise = &apitypes.SyntheticNoise{Epsilon: 10}
	rep, dir := mustRun(t, opts{spec: spec, source: src})
	db := lifeQuery(t, src, Layout{Dir: dir, Name: "crmsynth"}.Snapshot(rep.BuildID))
	const age = "date_sub('year', birth, DATE '2024-06-30')"
	if n := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.life WHERE "+age+" < 35"); n != 0 {
		t.Errorf("%d rows with an age the clip drops", n)
	}
	if n := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.life WHERE "+age+" >= 60"); n == 0 {
		t.Error("no rows with an age of 60 or more, the clip cut the wrong end")
	}
}
