// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"

	_ "github.com/bino-bi/sluice/internal/datasource/drivers/duckdbfile"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

const (
	seedRef   = "secret://env/SYN_SEED"
	testSeed  = "build-test-seed-0123456789abcdef"
	otherSeed = "build-test-seed-fedcba9876543210"
)

// fixtureSQL builds crm.customers with fixed formulas (plan 14). gender:
// X 3 rows (rare), D 397, W 10 000, M 9 600. revenue has two values far
// above the rest (a spike). score is a permutation of 0 to 1999.9.
// extraCol adds one more column at the end. crm.people is the table of
// the correlation tests (plan 11).
func fixtureSQL(extraCol bool) string {
	extra := ""
	if extraCol {
		extra = ", CAST(i % 7 AS INTEGER) AS extra"
	}
	return `
CREATE SCHEMA crm;
CREATE TYPE crm.g AS ENUM ('M', 'W', 'D', 'X');
CREATE TABLE crm.customers AS
SELECT
  CAST(i AS BIGINT) AS id,
  CAST(CASE WHEN i < 3 THEN 'X' WHEN i < 400 THEN 'D' WHEN i < 10400 THEN 'W' ELSE 'M' END AS crm.g) AS gender,
  ['retail', 'smb', 'enterprise'][i % 3 + 1] AS segment,
  TIME '06:00:00' + ((i * 37) % 43200) * INTERVAL 1 SECOND AS opened,
  TIMESTAMPTZ '2024-01-01 00:00:00+00' + ((i * 7919) % 31536000) * INTERVAL 1 SECOND AS last_seen,
  CAST(CASE WHEN i = 0 THEN 990000 WHEN i = 1 THEN 989000
       ELSE pow(((i * 7919) % 20000) / 20000.0, 3) * 5000 + 10 END AS DECIMAL(12,2)) AS revenue,
  'Nam' || substr('abcdefghijklmnopqrstuvwxyz', (i % 300) % 26 + 1, 1)
        || substr('abcdefghijklmnopqrstuvwxyz', (i % 300) // 26 + 1, 1)
        || repeat('e', (i % 300) % 7 + 1) AS last_name,
  'user' || i || '@example.com' AS email,
  DATE '1950-01-01' + CAST((i * 7919) % 18000 AS INTEGER) AS birth_date,
  ['alpha beta', 'gamma delta epsilon', 'zeta'][i % 3 + 1] AS notes,
  CAST((i * 7919) % 20000 AS DOUBLE) / 10 AS score,
  [i % 5] AS attrs` + extra + `
FROM range(20000) r(i);
CREATE TABLE crm.people AS SELECT
  CAST(20 + (i * 7919) % 50 AS INTEGER) AS age,
  CAST(1000 * (20 + (i * 7919) % 50) + (i * 104729) % 20000 AS DOUBLE) AS income,
  CAST(100 - (20 + (i * 7919) % 50) + (i * 31) % 10 AS DOUBLE) AS risk,
  CAST((i * 48271) % 10007 AS DOUBLE) AS noise_col,
  DATE '2000-01-01' + CAST((20 + (i * 7919) % 50) * 30 + i % 30 AS INTEGER) AS joined,
  CASE WHEN i < 2 THEN 'gold' WHEN i < 4002 THEN 'silver' ELSE 'bronze' END AS tier,
  CAST(CASE WHEN i = 0 THEN 990000 ELSE (i * 7919) % 5000 END AS DOUBLE) AS bonus
FROM range(20000) r(i);
`
}

var (
	fixtureOnce sync.Map // bool -> *fixtureResult
	fixtureDir  string
)

type fixtureResult struct {
	once sync.Once
	path string
	err  error
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "synth-build-fixture-")
	if err != nil {
		panic(err)
	}
	fixtureDir = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// fixturePath returns the path of the fixture database, created once.
func fixturePath(t *testing.T, extraCol bool) string {
	t.Helper()
	v, _ := fixtureOnce.LoadOrStore(extraCol, &fixtureResult{})
	f := v.(*fixtureResult)
	f.once.Do(func() {
		f.path = filepath.Join(fixtureDir, fmt.Sprintf("crm-%v.duckdb", extraCol))
		c, err := duckdb.NewConnector(f.path, nil)
		if err != nil {
			f.err = err
			return
		}
		db := sql.OpenDB(c)
		defer func() { _ = db.Close() }()
		_, f.err = db.Exec(fixtureSQL(extraCol))
	})
	if f.err != nil {
		t.Fatalf("fixture: %v", f.err)
	}
	return f.path
}

type mapSecrets map[string][]byte

func (m mapSecrets) Resolve(_ context.Context, ref string) ([]byte, error) {
	v, ok := m[ref]
	if !ok {
		return nil, errors.New("secret not found")
	}
	return v, nil
}

type mapTagger map[string][]string

func (m mapTagger) Tags(_, _, _, column string) []string { return m[column] }

func f64(v float64) *float64 { return &v }
func i64(v int64) *int64     { return &v }
func intp(v int) *int        { return &v }

// baseSpec is the main spec: explicit generators for most columns, the
// pii tag rule for email, defaults for gender, notes, birth_date, attrs.
func baseSpec() *apitypes.SyntheticSpec {
	return &apitypes.SyntheticSpec{
		Source:  "crm",
		Seed:    seedRef,
		Privacy: &apitypes.SyntheticPrivacy{MinGroupSize: intp(5)},
		Rules: []apitypes.SyntheticRule{{
			Match:     apitypes.SyntheticMatch{Tags: []string{"pii"}},
			Generator: apitypes.GenFake,
			Args:      map[string]any{"kind": "email"},
		}},
		Tables: []apitypes.SyntheticTable{{
			Name: "crm.customers",
			Size: apitypes.SyntheticSize{Ratio: f64(0.2)},
			Columns: map[string]apitypes.SyntheticColumn{
				"id":        {Generator: apitypes.GenSequence},
				"segment":   {Generator: apitypes.GenWeighted, Args: map[string]any{"values": map[string]any{"retail": 1.0, "smb": 2.0, "enterprise": 3.0}}},
				"last_name": {Generator: apitypes.GenRandomString, Args: map[string]any{"length": "source"}},
				"revenue":   {Generator: apitypes.GenDistribution, Args: map[string]any{"decimals": 2.0}},
				"opened":    {Generator: apitypes.GenDistribution},
				"last_seen": {Generator: apitypes.GenDateDistribution},
				"score":     {Generator: apitypes.GenDistribution},
			},
		}},
	}
}

type opts struct {
	spec     *apitypes.SyntheticSpec
	dir      string
	seed     string
	maxRatio float64
	extraCol bool
	source   string // path of the source database, default the fixture
	tagger   Tagger
	clock    func() time.Time
	log      *slog.Logger
}

func run(t *testing.T, o opts) (*Report, error) {
	t.Helper()
	if o.spec == nil {
		o.spec = baseSpec()
	}
	if o.dir == "" {
		o.dir = t.TempDir()
	}
	if o.seed == "" {
		o.seed = testSeed
	}
	if o.tagger == nil {
		o.tagger = mapTagger{"email": {"pii"}}
	}
	if o.log == nil {
		o.log = slog.New(slog.DiscardHandler)
	}
	if o.source == "" {
		o.source = fixturePath(t, o.extraCol)
	}
	in := Input{
		DataSource: &apitypes.DataSource{
			Metadata: apitypes.ObjectMeta{Name: "crmsynth"},
			Spec:     apitypes.DataSourceSpec{Type: apitypes.DSSynthetic, Synthetic: o.spec},
		},
		Source: &apitypes.DataSource{
			Metadata: apitypes.ObjectMeta{Name: "crm"},
			Spec:     apitypes.DataSourceSpec{Type: apitypes.DSDuckDBFile, Path: o.source},
		},
		Secrets:  mapSecrets{seedRef: []byte(o.seed)},
		Tagger:   o.tagger,
		Dir:      o.dir,
		MaxRatio: o.maxRatio,
		Harden:   executor.HardenConfig{Threads: 4},
		Origin:   "cli",
		Clock:    o.clock,
		Log:      o.log,
	}
	return Run(context.Background(), in)
}

func mustRun(t *testing.T, o opts) (*Report, string) {
	t.Helper()
	if o.dir == "" {
		o.dir = t.TempDir()
	}
	rep, err := run(t, o)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return rep, o.dir
}

// mainBuild is the build of baseSpec, shared by the read-only tests.
var mainBuild struct {
	once sync.Once
	rep  *Report
	dir  string
	err  error
}

func shared(t *testing.T) (*Report, Layout) {
	t.Helper()
	mainBuild.once.Do(func() {
		mainBuild.dir = filepath.Join(fixtureDir, "main")
		mainBuild.rep, mainBuild.err = run(t, opts{dir: mainBuild.dir})
	})
	if mainBuild.err != nil {
		t.Fatalf("main build: %v", mainBuild.err)
	}
	return mainBuild.rep, Layout{Dir: mainBuild.dir, Name: "crmsynth"}
}

// query opens an in-memory DuckDB with the source as src and the snapshot
// as syn, both read-only.
func query(t *testing.T, snapshot string) *sql.DB {
	t.Helper()
	c, err := duckdb.NewConnector("", nil)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, s := range []string{
		"ATTACH '" + fixturePath(t, false) + "' AS src (READ_ONLY)",
		"ATTACH '" + snapshot + "' AS syn (READ_ONLY)",
		"SET TimeZone = 'UTC'",
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return db
}

func scalar[T any](t *testing.T, db *sql.DB, q string) T {
	t.Helper()
	var v T
	if err := db.QueryRow(q).Scan(&v); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return v
}

func shares(t *testing.T, db *sql.DB, q string) map[string]float64 {
	t.Helper()
	rows, err := db.Query(q)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]float64{}
	for rows.Next() {
		var k string
		var v float64
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		out[k] = v
	}
	return out
}

func reportColumn(t *testing.T, rep *Report, name string) ColumnReport {
	t.Helper()
	for _, c := range rep.Tables[0].Columns {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no column %s in the report", name)
	return ColumnReport{}
}

// Test 1.
func TestGenderShares(t *testing.T) {
	rep, l := shared(t)
	db := query(t, l.Snapshot(rep.BuildID))
	const share = "SELECT CAST(gender AS VARCHAR), count(*) / sum(count(*)) OVER () FROM %s.crm.customers GROUP BY 1"
	src := shares(t, db, fmt.Sprintf(share, "src"))
	syn := shares(t, db, fmt.Sprintf(share, "syn"))
	for _, g := range []string{"M", "W", "D"} {
		if d := math.Abs(src[g] - syn[g]); d > 0.03 {
			t.Errorf("gender %s: source share %.4f, synthetic %.4f", g, src[g], syn[g])
		}
	}
	if c := reportColumn(t, rep, "gender"); c.Generator != "categorical" || c.ChosenBy != "default" || c.Distance == nil || c.Distance.Metric != "tv" {
		t.Errorf("gender report = %+v", c)
	}
}

// Test 2.
func TestRareValueSuppressed(t *testing.T) {
	rep, l := shared(t)
	db := query(t, l.Snapshot(rep.BuildID))
	if n := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.customers WHERE gender = 'X'"); n != 0 {
		t.Errorf("X appears %d times", n)
	}
	typ := scalar[string](t, db, "SELECT data_type FROM information_schema.columns WHERE table_catalog = 'syn' AND column_name = 'gender'")
	if typ != "VARCHAR" {
		t.Errorf("gender type = %s, want VARCHAR", typ)
	}
	if c := reportColumn(t, rep, "gender"); c.SuppressedValues != 1 || c.Type != "ENUM" {
		t.Errorf("gender report: suppressed_values = %d, type = %q", c.SuppressedValues, c.Type)
	}
	report, err := os.ReadFile(l.Report(rep.BuildID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(report), "'X'") {
		t.Error("the report holds the rare label X")
	}
}

// The ENUM type text lists every label, also the rare ones, so a type
// error must not print it.
func TestTypeErrorHidesLabels(t *testing.T) {
	spec := baseSpec()
	spec.Tables[0].Columns["gender"] = apitypes.SyntheticColumn{Generator: apitypes.GenDistribution}
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	dir := t.TempDir()
	rep, err := run(t, opts{spec: spec, dir: dir, log: log})
	if err == nil || !strings.Contains(err.Error(), "ENUM") {
		t.Fatalf("distribution on ENUM: err = %v", err)
	}
	report, rerr := os.ReadFile(Layout{Dir: dir, Name: "crmsynth"}.Report(rep.BuildID))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if text := err.Error() + string(report) + buf.String(); strings.Contains(text, "'X'") {
		t.Errorf("the error, report or log holds the rare label X: %v", err)
	}
}

// A clip below k/n must not put a value of fewer than k rows in the
// report.
func TestClipReportFloor(t *testing.T) {
	spec := baseSpec()
	spec.Tables[0].Privacy = &apitypes.SyntheticPrivacy{ClipOutliers: &apitypes.ClipOutliers{Lower: 0, Upper: 1}}
	rep, _ := mustRun(t, opts{spec: spec})
	c := reportColumn(t, rep, "revenue")
	// The two largest revenues, 990000 and 989000, are one row each.
	if c.Source.Max == nil || *c.Source.Max >= 989000 {
		t.Errorf("report source max is a raw extreme: %+v", c.Source)
	}
}

// Test 3 (build part) and test 4.
func TestBoundsMovedMedianClose(t *testing.T) {
	rep, l := shared(t)
	db := query(t, l.Snapshot(rep.BuildID))
	var srcMin, srcMax, srcMed, synMin, synMax, synMed float64
	const q = "SELECT min(score), max(score), quantile_cont(score, 0.5) FROM %s.crm.customers"
	if err := db.QueryRow(fmt.Sprintf(q, "src")).Scan(&srcMin, &srcMax, &srcMed); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(fmt.Sprintf(q, "syn")).Scan(&synMin, &synMax, &synMed); err != nil {
		t.Fatal(err)
	}
	r := srcMax - srcMin
	if synMin == srcMin || synMax == srcMax {
		t.Errorf("synthetic bounds equal the source: min %v max %v", synMin, synMax)
	}
	if math.Abs(synMin-srcMin) > 0.03*r || math.Abs(synMax-srcMax) > 0.03*r {
		t.Errorf("bounds moved too far: source [%v, %v], synthetic [%v, %v]", srcMin, srcMax, synMin, synMax)
	}
	if math.Abs(synMed-srcMed) > 0.02*r {
		t.Errorf("median: source %v, synthetic %v", srcMed, synMed)
	}
	c := reportColumn(t, rep, "score")
	if c.Source.Min == nil || *c.Source.Min == srcMin || *c.Source.Max == srcMax {
		t.Errorf("report source min/max must not be the raw extremes: %+v", c.Source)
	}
	if c.Distance == nil || c.Distance.Metric != "ks" || c.Distance.Value > 0.05 {
		t.Errorf("score distance = %+v", c.Distance)
	}
}

// Test 5.
func TestRandomStrings(t *testing.T) {
	rep, l := shared(t)
	db := query(t, l.Snapshot(rep.BuildID))
	if n := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.customers s JOIN src.crm.customers t USING (last_name)"); n != 0 {
		t.Errorf("%d synthetic last names match a source name", n)
	}
	const share = "SELECT CAST(length(last_name) AS VARCHAR), count(*) / sum(count(*)) OVER () FROM %s.crm.customers GROUP BY 1"
	src := shares(t, db, fmt.Sprintf(share, "src"))
	syn := shares(t, db, fmt.Sprintf(share, "syn"))
	for k, v := range src {
		if math.Abs(v-syn[k]) > 0.03 {
			t.Errorf("length %s: source share %.4f, synthetic %.4f", k, v, syn[k])
		}
	}
}

// Test 6.
func TestSizeChecks(t *testing.T) {
	spec := baseSpec()
	spec.Tables[0].Size = apitypes.SyntheticSize{Ratio: f64(0.3)}
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	// The base query on this source fails, so a profile query before the
	// ratio check shows in the debug log.
	rep, err := run(t, opts{spec: spec, log: log, source: brokenSource(t)})
	if err == nil || !strings.Contains(err.Error(), "maxRatio") {
		t.Fatalf("ratio 0.3: err = %v", err)
	}
	if rep.Result != ResultFailed || len(rep.Tables) != 0 {
		t.Errorf("report = %+v", rep)
	}
	if strings.Contains(buf.String(), "query failed") {
		t.Errorf("a profile query ran before the ratio check:\n%s", buf.String())
	}
	if _, err := run(t, opts{source: brokenSource(t)}); err == nil {
		t.Fatal("the base query on the broken source did not fail")
	}

	spec = baseSpec()
	spec.Tables[0].Size = apitypes.SyntheticSize{Rows: i64(4001)}
	if _, err := run(t, opts{spec: spec}); err == nil || !strings.Contains(err.Error(), "size.rows") {
		t.Fatalf("rows over the cap: err = %v", err)
	}
	spec.Tables[0].Size = apitypes.SyntheticSize{Rows: i64(4001)}
	if _, err := run(t, opts{spec: spec, maxRatio: 0.3}); err != nil {
		t.Fatalf("rows under a larger cap: %v", err)
	}
}

// Test 7.
func TestStrictTags(t *testing.T) {
	spec := baseSpec()
	spec.Rules = nil
	_, err := run(t, opts{spec: spec})
	if err == nil || !strings.Contains(err.Error(), "column email") || !strings.Contains(err.Error(), "pii") {
		t.Fatalf("email without a choice: err = %v", err)
	}

	// The tag rule of baseSpec is a choice.
	if _, err := run(t, opts{tagger: mapTagger{"email": {"pii"}, "notes": {"pii"}}, spec: func() *apitypes.SyntheticSpec {
		s := baseSpec()
		s.Rules[0].Generator = apitypes.GenRedact
		s.Rules[0].Args = nil
		return s
	}()}); err != nil {
		t.Fatalf("with a tag rule: %v", err)
	}

	spec = baseSpec()
	spec.Rules = nil
	spec.Privacy.StrictTags = []string{"secret"}
	_, err = run(t, opts{spec: spec, tagger: mapTagger{"email": {"pii"}, "notes": {"secret"}}})
	if err == nil || !strings.Contains(err.Error(), "column notes") || strings.Contains(err.Error(), "column email") {
		t.Fatalf("custom strictTags: err = %v", err)
	}
}

// brokenSource returns a copy of the fixture where crm.customers is a
// view on a dropped table: the columns are listed, but every query on
// the view fails.
func brokenSource(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "broken.duckdb")
	c, err := duckdb.NewConnector(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(fixtureSQL(false) + `
ALTER TABLE crm.customers RENAME TO gone;
CREATE VIEW crm.customers AS SELECT * FROM crm.gone;
DROP TABLE crm.gone;`); err != nil {
		t.Fatal(err)
	}
	return path
}

func rowsOf(t *testing.T, path, table, cols string) []string {
	t.Helper()
	db := query(t, path)
	rows, err := db.Query("SELECT " + cols + " FROM syn." + table + " ORDER BY rowid")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	n := len(strings.Split(cols, ","))
	var out []string
	for rows.Next() {
		vals := make([]any, n)
		ptrs := make([]any, n)
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprint(vals...))
	}
	return out
}

const allColumns = "id, gender, segment, opened, last_seen, revenue, last_name, email, birth_date, notes, score, attrs"

func equalRows(t *testing.T, what string, a, b []string) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("%s: %d rows and %d rows", what, len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("%s: row %d differs:\n%s\n%s", what, i, a[i], b[i])
		}
	}
}

// Test 8.
func TestDeterminism(t *testing.T) {
	first, _ := shared(t)
	_, l := shared(t)
	second, dir := mustRun(t, opts{})
	if first.ContentHash != second.ContentHash || first.ContentHash == "" {
		t.Fatalf("content hash differs: %s and %s", first.ContentHash, second.ContentHash)
	}
	l2 := Layout{Dir: dir, Name: "crmsynth"}
	equalRows(t, "two builds", rowsOf(t, l.Snapshot(first.BuildID), "crm.customers", allColumns),
		rowsOf(t, l2.Snapshot(second.BuildID), "crm.customers", allColumns))

	other, _ := mustRun(t, opts{seed: otherSeed})
	if other.ContentHash == first.ContentHash {
		t.Error("another seed gives the same content hash")
	}

	extra, dir3 := mustRun(t, opts{extraCol: true})
	l3 := Layout{Dir: dir3, Name: "crmsynth"}
	equalRows(t, "extra column", rowsOf(t, l.Snapshot(first.BuildID), "crm.customers", allColumns),
		rowsOf(t, l3.Snapshot(extra.BuildID), "crm.customers", allColumns))

	t.Setenv("TZ", "America/New_York")
	ny, _ := mustRun(t, opts{})
	if ny.ContentHash != first.ContentHash {
		t.Error("the host time zone changes the content hash")
	}

	if _, err := run(t, opts{seed: "short-seed"}); err == nil || !strings.Contains(err.Error(), "fewer than 16 bytes") {
		t.Errorf("short seed: err = %v", err)
	}
}

// Test 9.
func TestSpike(t *testing.T) {
	rep, _ := shared(t)
	var found bool
	for _, s := range rep.Spikes {
		if s.Column == "revenue" && s.Side == "high" && s.Table == "crm.customers" {
			found = true
		}
		if s.Column == "score" {
			t.Errorf("unexpected spike %+v", s)
		}
	}
	if !found {
		t.Errorf("spikes = %+v, want revenue high", rep.Spikes)
	}

	spec := baseSpec()
	c := spec.Tables[0].Columns["revenue"]
	c.Privacy = &apitypes.SyntheticPrivacy{ClipOutliers: &apitypes.ClipOutliers{Lower: 0.005, Upper: 0.995}}
	spec.Tables[0].Columns["revenue"] = c
	clipped, _ := mustRun(t, opts{spec: spec})
	for _, s := range clipped.Spikes {
		if s.Column == "revenue" {
			t.Errorf("spike with clipOutliers: %+v", s)
		}
	}
}

// Test 26.
func TestTypes(t *testing.T) {
	rep, l := shared(t)
	db := query(t, l.Snapshot(rep.BuildID))
	rows, err := db.Query("SELECT table_schema, column_name, data_type FROM information_schema.columns " +
		"WHERE table_catalog = 'syn' ORDER BY ordinal_position")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	got := map[string]string{}
	var order []string
	for rows.Next() {
		var schema, name, typ string
		if err := rows.Scan(&schema, &name, &typ); err != nil {
			t.Fatal(err)
		}
		if schema != "crm" {
			t.Errorf("schema = %s, want crm", schema)
		}
		got[name] = typ
		order = append(order, name)
	}
	want := map[string]string{
		"id": "BIGINT", "gender": "VARCHAR", "opened": "TIME", "last_seen": "TIMESTAMP WITH TIME ZONE",
		"revenue": "DECIMAL(12,2)", "birth_date": "DATE", "score": "DOUBLE", "attrs": "BIGINT[]",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: type %s, want %s", k, got[k], v)
		}
	}
	if strings.Join(order, ", ") != allColumns {
		t.Errorf("column order = %v", order)
	}
	if n := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.customers"); n != 4000 {
		t.Errorf("rows = %d, want 4000", n)
	}
	// opened is TIME between 06:00 and 18:00 in the source.
	if n := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.customers WHERE opened < TIME '05:30:00' OR opened > TIME '18:30:00'"); n != 0 {
		t.Errorf("%d opened values far outside the source range", n)
	}
	if n := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.customers WHERE attrs IS NOT NULL"); n != 0 {
		t.Errorf("attrs has %d values, want NULL", n)
	}
	if len(rep.Warnings) != 1 || !strings.HasPrefix(rep.Warnings[0], "crm.customers.attrs: ") {
		t.Errorf("warnings = %v", rep.Warnings)
	}
	tr := rep.Tables[0]
	if tr.SourceRows != 20000 || tr.SyntheticRows != 4000 || tr.Ratio != 0.2 {
		t.Errorf("table report = %+v", tr)
	}
}

// Test 24.
func TestEmptyTable(t *testing.T) {
	spec := baseSpec()
	spec.Rules = nil
	spec.Tables[0].Size = apitypes.SyntheticSize{Rows: i64(0)}
	// A choice that does not fit the type would fail resolution, and
	// email (pii) has no choice: rows 0 skips both.
	spec.Tables[0].Columns["notes"] = apitypes.SyntheticColumn{Generator: apitypes.GenDistribution}
	rep, dir := mustRun(t, opts{spec: spec})
	l := Layout{Dir: dir, Name: "crmsynth"}
	db := query(t, l.Snapshot(rep.BuildID))
	if n := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.customers"); n != 0 {
		t.Errorf("rows = %d", n)
	}
	src := scalar[string](t, db, "SELECT string_agg(column_name || ' ' || data_type, ', ' ORDER BY ordinal_position) "+
		"FROM information_schema.columns WHERE table_catalog = 'src' AND table_name = 'customers' AND column_name <> 'gender'")
	syn := scalar[string](t, db, "SELECT string_agg(column_name || ' ' || data_type, ', ' ORDER BY ordinal_position) "+
		"FROM information_schema.columns WHERE table_catalog = 'syn' AND table_name = 'customers' AND column_name <> 'gender'")
	if src != syn {
		t.Errorf("types differ:\n%s\n%s", src, syn)
	}
	if c := reportColumn(t, rep, "email"); c.Generator != "" || c.Source == nil {
		t.Errorf("email report = %+v", c)
	}
}

// Test 17.
func TestPublish(t *testing.T) {
	dir := t.TempDir()
	l := Layout{Dir: dir, Name: "crmsynth"}
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	clock := func(d time.Duration) func() time.Time { return func() time.Time { return at.Add(d) } }

	var ids []string
	for i := range 3 {
		rep, _ := mustRun(t, opts{dir: dir, clock: clock(time.Duration(i) * time.Hour)})
		ids = append(ids, rep.BuildID)
	}
	cur, err := l.ReadCurrent()
	if err != nil || cur != ids[2] {
		t.Fatalf("current = %q, %v; want %s", cur, err, ids[2])
	}
	if _, err := os.Stat(l.Snapshot(ids[0])); !os.IsNotExist(err) {
		t.Errorf("oldest snapshot kept: %v", err)
	}
	if _, err := os.Stat(l.Report(ids[0])); !os.IsNotExist(err) {
		t.Errorf("oldest report kept: %v", err)
	}
	for _, id := range ids[1:] {
		if _, err := os.Stat(l.Snapshot(id)); err != nil {
			t.Errorf("snapshot %s: %v", id, err)
		}
	}
	fi, err := os.Stat(l.Report(ids[2]))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("report mode = %v, %v", fi, err)
	}
	assertNoTemp(t, l)

	// A failed check leaves current and removes the temp files.
	spec := baseSpec()
	spec.Tables[0].Size = apitypes.SyntheticSize{Rows: i64(5000)}
	rep, err := run(t, opts{dir: dir, spec: spec, clock: clock(3 * time.Hour)})
	if err == nil {
		t.Fatal("over the cap: no error")
	}
	if cur, _ := l.ReadCurrent(); cur != ids[2] {
		t.Errorf("current moved to %s", cur)
	}
	if got, err := l.ReadReport(rep.BuildID); err != nil || got.Result != ResultFailed || got.Error == "" {
		t.Errorf("failed report = %+v, %v", got, err)
	}
	if _, err := os.Stat(l.Snapshot(rep.BuildID)); !os.IsNotExist(err) {
		t.Errorf("failed snapshot exists: %v", err)
	}
	assertNoTemp(t, l)

	// An older build that finishes after a newer one fails.
	rep, err = run(t, opts{dir: dir, clock: clock(-time.Hour)})
	if !errors.Is(err, ErrNewerCurrent) {
		t.Fatalf("older build: err = %v", err)
	}
	if cur, _ := l.ReadCurrent(); cur != ids[2] {
		t.Errorf("current moved to %s", cur)
	}
	if _, err := os.Stat(l.Snapshot(ids[2])); err != nil {
		t.Errorf("current snapshot: %v", err)
	}
	if _, err := os.Stat(l.Snapshot(rep.BuildID)); !os.IsNotExist(err) {
		t.Errorf("older snapshot kept: %v", err)
	}
	if got, err := l.ReadReport(rep.BuildID); err != nil || got.Result != ResultFailed {
		t.Errorf("older report = %+v, %v", got, err)
	}
	assertNoTemp(t, l)
}

func assertNoTemp(t *testing.T, l Layout) {
	t.Helper()
	for _, d := range []string{l.SnapshotsDir(), l.ReportsDir(), l.Root()} {
		entries, err := os.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".tmp") || strings.HasSuffix(e.Name(), ".wal") {
				t.Errorf("left over: %s", filepath.Join(d, e.Name()))
			}
		}
	}
}

// Test 22 (build part).
func TestBuildIDChecks(t *testing.T) {
	dir := t.TempDir()
	l := Layout{Dir: dir, Name: "crmsynth"}
	if err := os.MkdirAll(l.Root(), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../x", "20261102T101500Z-a1b2c", "20261102T101500Z-A1B2C3", "not-a-build-id"} {
		if err := os.WriteFile(filepath.Join(l.Root(), "current"), []byte(bad+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := l.ReadCurrent()
		if !errors.Is(err, ErrInvalidBuildID) || strings.Contains(err.Error(), bad) {
			t.Errorf("current %q: err = %v", bad, err)
		}
		if _, err := l.ReadReport(bad); !errors.Is(err, ErrInvalidBuildID) {
			t.Errorf("report %q: err = %v", bad, err)
		}
	}
	// A build does not move a current it cannot read.
	if _, err := run(t, opts{dir: dir}); !errors.Is(err, ErrInvalidBuildID) {
		t.Errorf("build with a bad current: err = %v", err)
	}
	if !ValidBuildID("20261102T101500Z-a1b2c3") {
		t.Error("valid id refused")
	}
}

// Test 23.
func TestSeedNotLogged(t *testing.T) {
	failing := baseSpec()
	failing.Rules = nil
	for name, spec := range map[string]*apitypes.SyntheticSpec{"passing": baseSpec(), "failing": failing} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			dir := t.TempDir()
			rep, err := run(t, opts{spec: spec, dir: dir, log: log})
			if (err != nil) != (name == "failing") {
				t.Fatalf("err = %v", err)
			}
			report, rerr := os.ReadFile(Layout{Dir: dir, Name: "crmsynth"}.Report(rep.BuildID))
			if rerr != nil {
				t.Fatal(rerr)
			}
			text := buf.String() + string(report)
			if err != nil {
				text += err.Error()
			}
			for _, s := range []string{testSeed, hex.EncodeToString([]byte(testSeed))} {
				if strings.Contains(text, s) {
					t.Errorf("seed found in the log, report or error")
				}
			}
		})
	}
}

func TestSpecHash(t *testing.T) {
	a, err := SpecHash(baseSpec())
	if err != nil {
		t.Fatal(err)
	}
	s := baseSpec()
	s.Build.OnStart = apitypes.BuildIfMissing
	s.Privacy.SpikeWarning = new(bool)
	s.Privacy.StrictTags = []string{"secret"}
	s.Tables[0].Privacy = &apitypes.SyntheticPrivacy{SpikeWarning: new(bool)}
	if b, _ := SpecHash(s); b != a {
		t.Errorf("report-only fields change the hash")
	}
	// R11: a table privacy with only rejectExactMatches is not report-only.
	s.Tables[0].Privacy.RejectExactMatches = []string{"name"}
	withList, _ := SpecHash(s)
	if withList == a {
		t.Errorf("rejectExactMatches keeps the hash")
	}
	s.Tables[0].Privacy.RejectExactMatches = []string{"name", "region"}
	if b, _ := SpecHash(s); b == withList {
		t.Errorf("a rejectExactMatches change keeps the hash")
	}
	s.Tables[0].Columns["score"] = apitypes.SyntheticColumn{Generator: apitypes.GenUniform}
	if b, _ := SpecHash(s); b == a {
		t.Errorf("a generator change keeps the hash")
	}
	if !strings.HasPrefix(a, "sha256:") {
		t.Errorf("hash = %s", a)
	}
}

// A level with only noise or only bounds is not report-only.
func TestSpecHashNoise(t *testing.T) {
	hash := func(s *apitypes.SyntheticSpec) string {
		t.Helper()
		h, err := SpecHash(s)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	s := baseSpec()
	s.Privacy = nil
	none := hash(s)
	s.Privacy = &apitypes.SyntheticPrivacy{Noise: &apitypes.SyntheticNoise{Epsilon: 1}}
	eps1 := hash(s)
	if eps1 == none {
		t.Errorf("noise keeps the hash")
	}
	s.Privacy.Noise.Epsilon = 2
	eps2 := hash(s)
	if eps2 == eps1 {
		t.Errorf("an epsilon change keeps the hash")
	}
	col := s.Tables[0].Columns["revenue"]
	col.Privacy = &apitypes.SyntheticPrivacy{Bounds: &apitypes.SyntheticBounds{Min: 0.0, Max: 6000.0}}
	s.Tables[0].Columns["revenue"] = col
	withBounds := hash(s)
	if withBounds == eps2 {
		t.Errorf("bounds keep the hash")
	}
	col.Privacy.Bounds.Max = 7000.0
	if hash(s) == withBounds {
		t.Errorf("a bounds change keeps the hash")
	}
}

func TestNoiseNotSupportedYet(t *testing.T) {
	spec := baseSpec()
	spec.Privacy.Noise = &apitypes.SyntheticNoise{Epsilon: 1}
	_, err := run(t, opts{spec: spec})
	if err == nil || !strings.Contains(err.Error(), "privacy.noise is not supported yet") {
		t.Fatalf("err = %v", err)
	}
}

// smallFixture writes a source with a nested ENUM and small integer
// ranges. rare has one row with 17, the rest is 0 to 9.
func smallFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "small.duckdb")
	c, err := duckdb.NewConnector(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`
CREATE SCHEMA crm;
CREATE TYPE crm.diag AS ENUM ('flu', 'cold', 'RARE_LABEL');
CREATE TABLE crm.t AS
SELECT
  {'d': CAST(CASE WHEN i = 0 THEN 'RARE_LABEL' WHEN i % 2 = 0 THEN 'flu' ELSE 'cold' END AS crm.diag)} AS st,
  MAP {'k': CAST('flu' AS crm.diag)} AS m,
  CAST(i % 2 AS HUGEINT) AS flag,
  CAST(i % 5 AS UBIGINT) AS small,
  CAST(i % 7 AS TINYINT) AS tiny,
  CAST(CASE WHEN i = 0 THEN 17 ELSE i % 10 END AS INTEGER) AS rare
FROM range(2000) r(i);
`); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return path
}

// A nested ENUM keeps no label in the snapshot schema, and a small
// integer range keeps its values.
func TestSmallFixture(t *testing.T) {
	src := smallFixture(t)
	spec := &apitypes.SyntheticSpec{
		Source:  "crm",
		Seed:    seedRef,
		Privacy: &apitypes.SyntheticPrivacy{MinGroupSize: intp(5)},
		Tables:  []apitypes.SyntheticTable{{Name: "crm.t", Size: apitypes.SyntheticSize{Ratio: f64(0.2)}}},
	}
	rep, dir := mustRun(t, opts{spec: spec, source: src})
	l := Layout{Dir: dir, Name: "crmsynth"}
	c, err := duckdb.NewConnector("", nil)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("ATTACH '" + l.Snapshot(rep.BuildID) + "' AS syn (READ_ONLY)"); err != nil {
		t.Fatal(err)
	}
	if n := scalar[int64](t, db, "SELECT count(*) FROM information_schema.columns WHERE table_catalog = 'syn' AND upper(data_type) LIKE '%ENUM(%'"); n != 0 {
		t.Errorf("%d snapshot columns with an ENUM type", n)
	}
	for col, want := range map[string]string{"flag": "0,1", "small": "0,1,2,3,4", "tiny": "0,1,2,3,4,5,6"} {
		got := scalar[string](t, db, "SELECT string_agg(DISTINCT CAST("+col+" AS VARCHAR), ',' ORDER BY CAST("+col+" AS VARCHAR)) FROM syn.crm.t")
		if got != want {
			t.Errorf("%s values = %s, want %s", col, got, want)
		}
		if g := reportColumn(t, rep, col).Generator; g != string(apitypes.GenCategorical) {
			t.Errorf("%s generator = %s, want categorical", col, g)
		}
	}
	if g := reportColumn(t, rep, "rare").Generator; g != string(apitypes.GenDistribution) {
		t.Errorf("rare generator = %s, want distribution", g)
	}
}

func TestSnapshotType(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"STRUCT(d ENUM('a', 'b'))", "VARCHAR"},
		{"MAP(VARCHAR, ENUM('a'))", "VARCHAR"},
		{"ENUM('a')", "VARCHAR"},
		{"STRUCT(d INTEGER)", "STRUCT(d INTEGER)"},
		{"INTEGER", "INTEGER"},
	} {
		if got := snapshotType(tc.in, apitypes.GenNull); got != tc.want {
			t.Errorf("snapshotType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
