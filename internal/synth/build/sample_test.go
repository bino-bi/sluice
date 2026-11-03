// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"database/sql"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// eventsFixture writes crm.events with 200 000 rows: cat is a 50%, b 30%,
// c about 20% and rare 40 rows. desc inserts the rows in reverse order.
func eventsFixture(t *testing.T, desc bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.duckdb")
	c, err := duckdb.NewConnector(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	defer func() { _ = db.Close() }()
	order := "i"
	if desc {
		order = "i DESC"
	}
	if _, err := db.Exec(`
CREATE SCHEMA crm;
CREATE TABLE crm.events AS
SELECT
  CAST(i AS BIGINT) AS id,
  CASE WHEN i % 5000 = 7 THEN 'rare' WHEN i % 10 < 5 THEN 'a' WHEN i % 10 < 8 THEN 'b' ELSE 'c' END AS cat,
  CAST(pow(((i * 7919) % 100000) / 100000.0, 2) * 1000 AS DOUBLE) AS amount,
  DATE '2020-01-01' + CAST((i * 104729) % 1460 AS INTEGER) AS d
FROM range(200000) r(i) ORDER BY ` + order + `;
`); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return path
}

func eventsSpec(sampleRows int64) *apitypes.SyntheticSpec {
	t := apitypes.SyntheticTable{
		Name: "crm.events",
		Size: apitypes.SyntheticSize{Ratio: f64(0.05)},
		Columns: map[string]apitypes.SyntheticColumn{
			"id":     {Generator: apitypes.GenSequence},
			"cat":    {Generator: apitypes.GenCategorical},
			"amount": {Generator: apitypes.GenDistribution},
			"d":      {Generator: apitypes.GenDateDistribution},
		},
	}
	if sampleRows > 0 {
		t.Profile = &apitypes.SyntheticProfile{SampleRows: &sampleRows}
	}
	return &apitypes.SyntheticSpec{
		Source:  "crm",
		Seed:    seedRef,
		Privacy: &apitypes.SyntheticPrivacy{MinGroupSize: intp(5)},
		Tables:  []apitypes.SyntheticTable{t},
	}
}

// nearRows reports whether a sample size is within 5% of want. The
// sample keeps each row by its hash, so its size varies.
func nearRows(got, want int64) bool {
	return math.Abs(float64(got-want)) <= 0.05*float64(want)
}

func snapshotDB(t *testing.T, dir string, rep *Report) *sql.DB {
	t.Helper()
	c, err := duckdb.NewConnector("", nil)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("ATTACH '" + Layout{Dir: dir, Name: "crmsynth"}.Snapshot(rep.BuildID) + "' AS syn (READ_ONLY)"); err != nil {
		t.Fatal(err)
	}
	return db
}

// Plan 5 tests 4, 5 and 8.
func TestSampleRows(t *testing.T) {
	src := eventsFixture(t, false)
	full, fullDir := mustRun(t, opts{spec: eventsSpec(0), source: src})
	rep, dir := mustRun(t, opts{spec: eventsSpec(10000), source: src})

	// Test 4: the base counts come from the sample, the size from the table.
	tr := rep.Tables[0]
	if !nearRows(tr.ProfiledRows, 10000) || tr.SourceRows != 200000 || tr.SyntheticRows != 10000 {
		t.Errorf("profiled %d, source %d, synthetic %d rows", tr.ProfiledRows, tr.SourceRows, tr.SyntheticRows)
	}
	if full.Tables[0].ProfiledRows != 0 {
		t.Errorf("full build profiled_rows = %d", full.Tables[0].ProfiledRows)
	}
	if d := reportColumn(t, rep, "id").Source.Distinct; d != tr.ProfiledRows {
		t.Errorf("id distinct = %d, want the %d of the sample", d, tr.ProfiledRows)
	}
	if !strings.Contains(strings.Join(rep.Warnings, "\n"), fmt.Sprintf("crm.events: profiled on a sample of %d rows", tr.ProfiledRows)) {
		t.Errorf("warnings = %v", rep.Warnings)
	}

	// Test 5: the sampled build is close to the full build.
	fdb, sdb := snapshotDB(t, fullDir, full), snapshotDB(t, dir, rep)
	const share = "SELECT cat, count(*) / sum(count(*)) OVER () FROM syn.crm.events GROUP BY 1"
	fs, ss := shares(t, fdb, share), shares(t, sdb, share)
	for _, k := range []string{"a", "b", "c"} {
		if math.Abs(fs[k]-ss[k]) > 0.02 {
			t.Errorf("cat %s: full %.3f, sampled %.3f", k, fs[k], ss[k])
		}
	}
	for _, q := range []string{
		"SELECT median(amount) / 1000 FROM syn.crm.events",
		"SELECT quantile_cont(amount, 0.9) / 1000 FROM syn.crm.events",
		"SELECT (epoch(quantile_disc(d, 0.25)) - epoch(DATE '2020-01-01')) / (1460 * 86400) FROM syn.crm.events",
	} {
		if f, s := scalar[float64](t, fdb, q), scalar[float64](t, sdb, q); math.Abs(f-s) > 0.02 {
			t.Errorf("%s: full %.4f, sampled %.4f", q, f, s)
		}
	}

	// Test 8: k counts in the sample. rare has 40 rows in the table and
	// about 2 in the sample, so it is not reproduced.
	if n := scalar[int64](t, sdb, "SELECT count(*) FROM syn.crm.events WHERE cat = 'rare'"); n != 0 {
		t.Errorf("%d rows of a value with fewer than minGroupSize sample rows", n)
	}
	if c := reportColumn(t, full, "cat"); c.SuppressedValues != 0 {
		t.Errorf("full build suppressed %d values, rare has 40 rows", c.SuppressedValues)
	}
}

// Plan 5 test 6: same sample for the same data in any row order.
func TestSampleRowsDeterministic(t *testing.T) {
	spec := eventsSpec(10000)
	a, _ := mustRun(t, opts{spec: spec, source: eventsFixture(t, false)})
	b, _ := mustRun(t, opts{spec: spec, source: eventsFixture(t, false)})
	c, _ := mustRun(t, opts{spec: spec, source: eventsFixture(t, true)})
	if a.ContentHash != b.ContentHash || a.ContentHash != c.ContentHash {
		t.Errorf("content hashes differ: %s, %s, reversed order %s", a.ContentHash, b.ContentHash, c.ContentHash)
	}
	other, _ := mustRun(t, opts{spec: spec, source: eventsFixture(t, false), seed: testSeed + "-other"})
	if other.ContentHash == a.ContentHash {
		t.Error("another seed gives the same snapshot")
	}
}

// Plan 5 test 7: a table with at most sampleRows rows reads the table.
func TestSampleRowsSmallTable(t *testing.T) {
	full, _ := mustRun(t, opts{})
	spec := baseSpec()
	n := int64(50000)
	spec.Tables[0].Profile = &apitypes.SyntheticProfile{SampleRows: &n}
	rep, _ := mustRun(t, opts{spec: spec})
	if rep.ContentHash != full.ContentHash {
		t.Errorf("content hash %s, want %s of the full build", rep.ContentHash, full.ContentHash)
	}
	if rep.Tables[0].ProfiledRows != 0 {
		t.Errorf("profiled_rows = %d", rep.Tables[0].ProfiledRows)
	}
}

// Plan 5 test 12: every type of the main fixture builds from a sample.
func TestSampleRowsTypes(t *testing.T) {
	spec := baseSpec()
	n := int64(5000)
	spec.Tables[0].Profile = &apitypes.SyntheticProfile{SampleRows: &n}
	rep, dir := mustRun(t, opts{spec: spec})
	if !nearRows(rep.Tables[0].ProfiledRows, 5000) {
		t.Fatalf("profiled_rows = %d, want about 5000", rep.Tables[0].ProfiledRows)
	}
	db := query(t, Layout{Dir: dir, Name: "crmsynth"}.Snapshot(rep.BuildID))
	frep, fl := shared(t)
	fdb := query(t, fl.Snapshot(frep.BuildID))
	const types = "SELECT string_agg(column_name || ' ' || data_type, ', ' ORDER BY ordinal_position) FROM information_schema.columns WHERE table_catalog = 'syn' AND table_name = 'customers'"
	if f, s := scalar[string](t, fdb, types), scalar[string](t, db, types); f != s {
		t.Errorf("types differ:\nfull    %s\nsampled %s", f, s)
	}
	// gender is an ENUM; its kept labels must come through the sample.
	const g = "SELECT CAST(gender AS VARCHAR), count(*) / sum(count(*)) OVER () FROM %s.crm.customers GROUP BY 1"
	compareShares(t, db, "gender", g, 0.03)
}

// Plan 5 test 9: noise adds one measurement for the full row count.
func TestSampleRowsNoise(t *testing.T) {
	plain, _ := mustRun(t, opts{spec: noiseSpec()})
	spec := noiseSpec()
	n := int64(5000)
	spec.Tables[0].Profile = &apitypes.SyntheticProfile{SampleRows: &n}
	a, _ := mustRun(t, opts{spec: spec})
	if a.Noise.Measurements != plain.Noise.Measurements+1 {
		t.Errorf("measurements = %d, want %d", a.Noise.Measurements, plain.Noise.Measurements+1)
	}
	if !nearRows(a.Tables[0].ProfiledRows, 5000) {
		t.Errorf("profiled_rows = %d, want about 5000", a.Tables[0].ProfiledRows)
	}
	b, _ := mustRun(t, opts{spec: spec})
	if a.ContentHash != b.ContentHash {
		t.Errorf("content hash differs: %s and %s", a.ContentHash, b.ContentHash)
	}

	// size.rows 0 reads no profile, so it spends no measurement and
	// builds no sample.
	empty := noiseSpec()
	zero := int64(0)
	empty.Tables[0].Size = apitypes.SyntheticSize{Rows: &zero}
	plainEmpty, _ := mustRun(t, opts{spec: empty})
	empty.Tables[0].Profile = &apitypes.SyntheticProfile{SampleRows: &n}
	e, _ := mustRun(t, opts{spec: empty})
	if e.Tables[0].ProfiledRows != 0 || e.Noise.Measurements != plainEmpty.Noise.Measurements {
		t.Errorf("rows 0: profiled_rows %d, measurements %d, want 0 and %d", e.Tables[0].ProfiledRows, e.Noise.Measurements, plainEmpty.Noise.Measurements)
	}
}

// Plan 5 test 10: fanOut: source needs the full child table.
func TestSampleRowsFanOut(t *testing.T) {
	n := int64(2000)
	spec := fkSpec(nil, sequenceID)
	spec.Tables[0].Profile = &apitypes.SyntheticProfile{SampleRows: &n}
	_, err := run(t, opts{spec: spec, source: relationsPath(t)})
	if err == nil || !strings.Contains(err.Error(), "column customer_id: fanOut: source and orphanRate: source need the full table") {
		t.Fatalf("err = %v", err)
	}
	spec = fkSpec(map[string]any{"fanOut": map[string]any{"min": 1.0, "max": 3.0}}, sequenceID)
	spec.Tables[0].Profile = &apitypes.SyntheticProfile{SampleRows: &n}
	// The parent is sampled too.
	spec.Tables[1].Profile = &apitypes.SyntheticProfile{SampleRows: &n}
	rep, dir := mustRun(t, opts{spec: spec, source: relationsPath(t)})
	for _, tr := range rep.Tables {
		if !nearRows(tr.ProfiledRows, n) {
			t.Errorf("%s profiled_rows = %d, want about %d", tr.Name, tr.ProfiledRows, n)
		}
	}
	db := relQuery(t, Layout{Dir: dir, Name: "crmsynth"}.Snapshot(rep.BuildID))
	if o := scalar[int64](t, db, orphanQuery); o != 0 {
		t.Errorf("%d orders without a customer", o)
	}
}

// Plan 5 test 11: rejectExactMatches compares with the full table, not
// with the sample. About 0.4% of the rows match a source row, and only a
// quarter of those would match a row of the 5000-row sample.
func TestSampleRowsExactMatches(t *testing.T) {
	spec := baseSpec()
	n := int64(5000)
	spec.Tables[0].Profile = &apitypes.SyntheticProfile{SampleRows: &n}
	spec.Tables[0].Privacy = &apitypes.SyntheticPrivacy{RejectExactMatches: []string{"last_name", "birth_date"}}
	spec.Tables[0].Columns["last_name"] = apitypes.SyntheticColumn{Generator: apitypes.GenCategorical}
	spec.Tables[0].Columns["birth_date"] = apitypes.SyntheticColumn{Generator: apitypes.GenDateDistribution}
	rep, dir := mustRun(t, opts{spec: spec})
	if em := rep.Tables[0].ExactMatches; em == nil || em.MatchedRows == 0 {
		t.Fatalf("exact_matches = %+v, the test needs matches", em)
	}
	db := query(t, Layout{Dir: dir, Name: "crmsynth"}.Snapshot(rep.BuildID))
	if m := scalar[int64](t, db, `SELECT count(*) FROM syn.crm.customers s WHERE EXISTS (SELECT 1 FROM src.crm.customers c
  WHERE c.last_name = s.last_name AND c.birth_date = s.birth_date)`); m != 0 {
		t.Errorf("%d synthetic rows equal a source row", m)
	}
}

// Equal rows are kept one by one, not as a block: a table of one
// BOOLEAN column gives a sample of about N rows with both values.
func TestSampleRowsDuplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dups.duckdb")
	c, err := duckdb.NewConnector(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	if _, err := db.Exec("CREATE SCHEMA crm; CREATE TABLE crm.flags AS SELECT i % 2 = 0 AS flag FROM range(10000) r(i)"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	n := int64(1000)
	spec := &apitypes.SyntheticSpec{
		Source:  "crm",
		Seed:    seedRef,
		Privacy: &apitypes.SyntheticPrivacy{MinGroupSize: intp(5)},
		Tables: []apitypes.SyntheticTable{{Name: "crm.flags", Size: apitypes.SyntheticSize{Ratio: f64(0.2)},
			Profile: &apitypes.SyntheticProfile{SampleRows: &n}}},
	}
	for _, seed := range []string{testSeed, testSeed + "-a", testSeed + "-b"} {
		rep, dir := mustRun(t, opts{spec: spec, source: path, seed: seed})
		if p := rep.Tables[0].ProfiledRows; math.Abs(float64(p-n)) > 0.1*float64(n) {
			t.Errorf("seed %s: profiled_rows = %d, want about %d", seed, p, n)
		}
		share := scalar[float64](t, snapshotDB(t, dir, rep), "SELECT avg(CAST(flag AS INTEGER)) FROM syn.crm.flags")
		if math.Abs(share-0.5) > 0.06 {
			t.Errorf("seed %s: true share %.3f, want about 0.5", seed, share)
		}
	}
}
