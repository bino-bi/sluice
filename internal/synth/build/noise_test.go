// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// baseM is M of baseSpec with noise, one line per column (plan 6.2).
const baseM = 1 + // rows
	1 + // id: nulls, sequence reads nothing
	2 + // gender: nulls, categorical (fallback null reads nothing)
	1 + // segment: nulls, weighted reads nothing
	2 + // opened: nulls, distribution
	2 + // last_seen: nulls, dateDistribution
	2 + // revenue: nulls, distribution
	3 + // last_name: nulls, randomString lengths and case styles
	1 + // email: nulls, fake email reads nothing
	2 + // birth_date: nulls, dateDistribution
	4 + // notes: nulls, categorical, randomString lengths and case styles
	2 + // score: nulls, distribution
	1 // attrs: nulls, null

func withNoise(spec *apitypes.SyntheticSpec, epsilon float64) *apitypes.SyntheticSpec {
	if spec.Privacy == nil {
		spec.Privacy = &apitypes.SyntheticPrivacy{}
	}
	spec.Privacy.Noise = &apitypes.SyntheticNoise{Epsilon: epsilon}
	return spec
}

func setBounds(spec *apitypes.SyntheticSpec, column string, lo, hi any) {
	c := spec.Tables[0].Columns[column]
	c.Privacy = &apitypes.SyntheticPrivacy{Bounds: &apitypes.SyntheticBounds{Min: lo, Max: hi}}
	spec.Tables[0].Columns[column] = c
}

// noiseSpec is the spec of test 15.
func noiseSpec() *apitypes.SyntheticSpec {
	spec := withNoise(baseSpec(), 5)
	setBounds(spec, "revenue", 0.0, 6000.0)
	setBounds(spec, "score", 0.0, 2000.0)
	return spec
}

// budgetOf runs planBudget alone on the fixture.
func budgetOf(t *testing.T, spec *apitypes.SyntheticSpec, tagger Tagger) (int, error) {
	t.Helper()
	b := &builder{
		in: Input{
			Source: &apitypes.DataSource{
				Metadata: apitypes.ObjectMeta{Name: "crm"},
				Spec:     apitypes.DataSourceSpec{Type: apitypes.DSDuckDBFile, Path: fixturePath(t, false)},
			},
			Secrets: mapSecrets{},
			Tagger:  tagger,
			Harden:  executor.HardenConfig{Threads: 1},
		},
		spec: spec,
		name: "crmsynth",
		log:  slog.New(slog.DiscardHandler),
	}
	conn, closeAll, err := b.open(context.Background(), filepath.Join(t.TempDir(), "out.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	return b.planBudget(context.Background(), conn)
}

// Test 14.
func TestNoiseBudget(t *testing.T) {
	pii := mapTagger{"email": {"pii"}}
	if m, err := budgetOf(t, withNoise(baseSpec(), 1), pii); err != nil || m != baseM {
		t.Errorf("baseSpec: M = %d, err = %v, want %d", m, err, baseM)
	}

	// A group of 3 adds 3 pairs. people: rows, 7 nulls, age, income, risk,
	// noise_col (distribution), joined (dateDistribution), tier
	// (categorical, randomString lengths and case styles), bonus
	// (distribution), 3 pairs.
	if m, err := budgetOf(t, withNoise(peopleSpec(), 1), pii); err != nil || m != 1+7+4+1+3+1+3 {
		t.Errorf("people: M = %d, err = %v, want 20", m, err)
	}

	// A part outside the whitelist (PartVocabulary) and by fail.
	spec := withNoise(baseSpec(), 1)
	spec.Tables[0].Columns["notes"] = apitypes.SyntheticColumn{Generator: apitypes.GenSourceVocabulary}
	spec.Tables[0].Columns["score"] = apitypes.SyntheticColumn{Generator: apitypes.GenDistribution, Args: map[string]any{"by": []any{"segment"}}}
	_, err := budgetOf(t, spec, pii)
	if err == nil || !strings.Contains(err.Error(), "column notes: generator sourceVocabulary is not supported with noise") ||
		!strings.Contains(err.Error(), "column score: by is not supported with noise") {
		t.Errorf("unsupported parts: err = %v", err)
	}

	// Without a rule the pii tag fails as in a build without noise.
	spec = withNoise(baseSpec(), 1)
	spec.Rules = nil
	_, err = budgetOf(t, spec, pii)
	plain := baseSpec()
	plain.Rules = nil
	_, want := run(t, opts{spec: plain})
	if err == nil || want == nil || err.Error() != want.Error() {
		t.Errorf("strictTags: planBudget err = %v, build err = %v", err, want)
	}

	// With a tag rule M counts the rule's generator.
	spec = withNoise(baseSpec(), 1)
	spec.Rules[0].Generator = apitypes.GenRandomString
	spec.Rules[0].Args = map[string]any{"length": "source"}
	if m, err := budgetOf(t, spec, pii); err != nil || m != baseM+2 {
		t.Errorf("tag rule: M = %d, err = %v, want %d", m, err, baseM+2)
	}

	// Bounds on a generator without a number part.
	spec = withNoise(baseSpec(), 1)
	setBounds(spec, "segment", 0.0, 1.0)
	if _, err := budgetOf(t, spec, pii); err == nil ||
		!strings.Contains(err.Error(), "column segment: privacy.bounds needs a generator that reads numbers") {
		t.Errorf("bounds on weighted: err = %v", err)
	}
}

// ksBelow is max |p_i - F(q_i)| of the grid q against the source values
// of e below limit.
func ksBelow(t *testing.T, db *sql.DB, e string, limit float64, q []float64) float64 {
	t.Helper()
	rows, err := db.Query("SELECT CAST(" + e + " AS DOUBLE) FROM src.crm.customers WHERE " + e + " < " +
		strconv.FormatFloat(limit, 'g', -1, 64) + " ORDER BY 1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var vals []float64
	for rows.Next() {
		var v float64
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		vals = append(vals, v)
	}
	var d float64
	s := len(q) - 1
	for i, x := range q {
		n, _ := slices.BinarySearch(vals, math.Nextafter(x, math.Inf(1)))
		d = math.Max(d, math.Abs(float64(i)/float64(s)-float64(n)/float64(len(vals))))
	}
	return d
}

// Test 15.
func TestNoiseBuild(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	rep, dir := mustRun(t, opts{spec: noiseSpec(), log: log})
	if n := rep.Noise; n == nil || n.Epsilon != 5 || n.Measurements != baseM || n.EpsilonPerMeasurement != 5/float64(baseM) {
		t.Fatalf("noise report = %+v, want M %d", rep.Noise, baseM)
	}
	if !strings.Contains(buf.String(), "measurements="+strconv.Itoa(baseM)) {
		t.Errorf("log has no budget line:\n%s", buf.String())
	}
	db := query(t, Layout{Dir: dir, Name: "crmsynth"}.Snapshot(rep.BuildID))
	const share = "SELECT CAST(gender AS VARCHAR), count(*) / sum(count(*)) OVER () FROM %s.crm.customers GROUP BY 1"
	src := shares(t, db, fmt.Sprintf(share, "src"))
	syn := shares(t, db, fmt.Sprintf(share, "syn"))
	for _, g := range []string{"M", "W", "D"} {
		if d := math.Abs(src[g] - syn[g]); d > 0.05 {
			t.Errorf("gender %s: source share %.4f, synthetic %.4f", g, src[g], syn[g])
		}
	}
	if mx := scalar[float64](t, db, "SELECT max(revenue) FROM syn.crm.customers"); mx > 6000 {
		t.Errorf("synthetic revenue max %v is above the bounds", mx)
	}
	for _, c := range []struct {
		name  string
		limit float64
	}{{"revenue", 6000}, {"score", 2000}} {
		cr := reportColumn(t, rep, c.name)
		if cr.Range != "bounds" || cr.Distance == nil || cr.Distance.Metric != "ks" {
			t.Errorf("%s report: range %q, distance %+v", c.name, cr.Range, cr.Distance)
			continue
		}
		// ks against the exact source below the bounds, on the synthetic
		// quantiles at i/100.
		var list any
		if err := db.QueryRow("SELECT quantile_cont(CAST(" + c.name + " AS DOUBLE), " + floatList(grid(100)) + ") FROM syn.crm.customers").Scan(&list); err != nil {
			t.Fatal(err)
		}
		q, err := floats(list)
		if err != nil {
			t.Fatal(err)
		}
		ks := ksBelow(t, db, c.name, c.limit, q)
		// Measured on amd64: revenue 0.050 (most rows sit in the first bins
		// of 30), score 0.009.
		if ks > 0.06 {
			t.Errorf("%s: ks %.4f against the source", c.name, ks)
		}
	}
	opened := reportColumn(t, rep, "opened")
	want := "crm.customers.opened: range is not covered by noise, set privacy.bounds"
	if opened.Range != "profile" || !slices.Contains(rep.Warnings, want) {
		t.Errorf("opened: range %q, warnings %q", opened.Range, rep.Warnings)
	}
	if c := reportColumn(t, rep, "gender"); c.Range != "" || c.NoiseMeasurements != 2 {
		t.Errorf("gender: range %q, noise_measurements %d", c.Range, c.NoiseMeasurements)
	}
	for _, s := range rep.Spikes {
		if s.Column == "revenue" {
			t.Errorf("spike with bounds: %+v", s)
		}
	}

	again, _ := mustRun(t, opts{spec: noiseSpec()})
	if again.ContentHash != rep.ContentHash {
		t.Errorf("content hash differs: %s and %s", rep.ContentHash, again.ContentHash)
	}

	t.Run("bounds on categorical", func(t *testing.T) {
		spec := noiseSpec()
		spec.Tables[0].Columns["gender"] = apitypes.SyntheticColumn{Generator: apitypes.GenCategorical,
			Privacy: &apitypes.SyntheticPrivacy{Bounds: &apitypes.SyntheticBounds{Min: 0.0, Max: 1.0}}}
		_, err := run(t, opts{spec: spec})
		if err == nil || !strings.Contains(err.Error(), "column gender: privacy.bounds needs a generator that reads numbers") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("correlated", func(t *testing.T) {
		rep, _ := mustRun(t, opts{spec: withNoise(peopleSpec(), 50)})
		cs := rep.Tables[0].Correlations
		if len(cs) != 1 || len(cs[0].Source) != 3 || cs[0].Source[0][1] < 0.5 || cs[0].Source[0][2] > -0.5 {
			t.Errorf("correlations = %+v", cs)
		}
	})
}

func grid(s int) []float64 {
	out := make([]float64, s+1)
	for i := range out {
		out[i] = float64(i) / float64(s)
	}
	return out
}

// dataFixture copies the fixture and runs stmt on the copy.
func dataFixture(t *testing.T, stmt string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "changed.duckdb")
	c, err := duckdb.NewConnector(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(fixtureSQL(false) + stmt); err != nil {
		t.Fatal(err)
	}
	return path
}

// noisyColumns are the columns of noiseSpec whose values come from a
// noisy profile.
var noisyColumns = []string{"gender", "opened", "last_seen", "revenue", "last_name", "birth_date", "notes", "score"}

// Test 16.
func TestNoiseDataKeyed(t *testing.T) {
	changed := dataFixture(t, "UPDATE crm.customers SET score = score * 1.01;")
	values := func(rep *Report, dir, col string) []string {
		return rowsOf(t, Layout{Dir: dir, Name: "crmsynth"}.Snapshot(rep.BuildID), "crm.customers", col)
	}

	a, dirA := mustRun(t, opts{spec: noiseSpec()})
	b, dirB := mustRun(t, opts{spec: noiseSpec(), source: changed})
	for _, col := range noisyColumns {
		if slices.Equal(values(a, dirA, col), values(b, dirB, col)) {
			t.Errorf("noise: column %s is the same after a change of score", col)
		}
	}
	same, _ := mustRun(t, opts{spec: noiseSpec()})
	if same.ContentHash != a.ContentHash {
		t.Errorf("same data: content hash %s and %s", a.ContentHash, same.ContentHash)
	}

	p, dirP := mustRun(t, opts{})
	q, dirQ := mustRun(t, opts{source: changed})
	for _, col := range noisyColumns {
		eq := slices.Equal(values(p, dirP, col), values(q, dirQ, col))
		if col == "score" && eq || col != "score" && !eq {
			t.Errorf("without noise: column %s equal = %v", col, eq)
		}
	}
}

// smallSpec is test 17: epsilon 0.001, a fixed row count, the pii rule
// and the given columns.
func smallSpec(cols map[string]apitypes.SyntheticColumn) *apitypes.SyntheticSpec {
	spec := withNoise(baseSpec(), 0.001)
	spec.Tables[0].Size = apitypes.SyntheticSize{Rows: i64(4000)}
	spec.Tables[0].Columns = cols
	return spec
}

// runSmall runs spec with the seeds noise-small-seed-00, -01 and on,
// and returns the first run that does not fail the size check and gives
// an error when fail is set, no error else. The noise is pinned by the
// seed and the data, so the seed it picks is the same on every run. At
// this epsilon about half the empty bins of 200 survive tau = b ln(200)
// by chance (plan 1.6), so a number column sometimes collapses into one
// random bin instead of failing.
func runSmall(t *testing.T, spec *apitypes.SyntheticSpec, fail bool, o opts) (*Report, string, error) {
	t.Helper()
	o.spec, o.maxRatio = spec, 1
	for i := range 20 {
		o.seed = fmt.Sprintf("noise-small-seed-%02d-0123456789", i)
		rep, err := run(t, o)
		if err != nil && strings.Contains(err.Error(), "maxRatio") || (err != nil) != fail {
			continue
		}
		return rep, o.seed, err
	}
	t.Fatal("no seed gives the wanted result")
	return nil, "", nil
}

// debugNoTime drops the time: its digits could match a searched count.
var debugNoTime = &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey {
		return slog.Attr{}
	}
	return a
}}

// exactCounts are counts of the fixture that must not show.
var exactCounts = []string{"20000", "10000", "9600", "397"}

// Test 17.
func TestNoiseSmallEpsilon(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, debugNoTime))
	_, _, err := runSmall(t, smallSpec(map[string]apitypes.SyntheticColumn{"score": {Generator: apitypes.GenDistribution}}), true, opts{log: log})
	if err == nil || !errors.Is(err, profile.ErrTooFewRows) && !errors.Is(err, profile.ErrGrid) || !strings.Contains(err.Error(), "column score") {
		t.Fatalf("err = %v", err)
	}
	for _, n := range exactCounts {
		if strings.Contains(err.Error(), n) || strings.Contains(buf.String(), n) {
			t.Errorf("count %s in the error or the log", n)
		}
	}

	rep, _, err := runSmall(t, smallSpec(nil), false, opts{})
	if err != nil {
		t.Fatalf("defaults: %v", err)
	}
	// A number default falls back to null, or keeps the bins that
	// survived tau by chance (see runSmall).
	nulls := 0
	for _, name := range []string{"opened", "revenue", "score"} {
		c := reportColumn(t, rep, name)
		switch {
		case c.Generator == "null" && c.ChosenBy == "default" && len(c.Warnings) > 0:
			nulls++
		case c.Generator == "distribution" && c.Range == "profile":
		default:
			t.Errorf("%s: generator %s, warnings %q", name, c.Generator, c.Warnings)
		}
	}
	if nulls == 0 {
		t.Error("no number default fell back to null")
	}
}

// srcDB opens an in-memory DuckDB with the fixture as src.
func srcDB(t *testing.T) *sql.DB {
	t.Helper()
	c, err := duckdb.NewConnector("", nil)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("ATTACH '" + fixturePath(t, false) + "' AS src (READ_ONLY)"); err != nil {
		t.Fatal(err)
	}
	return db
}

// fingerprintOf computes the source fingerprint of crm.customers as
// sourceFingerprint does.
func fingerprintOf(t *testing.T) string {
	t.Helper()
	db := srcDB(t)
	cols := "id, gender, segment, opened, last_seen, revenue, last_name, email, birth_date, notes, score, attrs"
	var n int64
	var sum string
	if err := db.QueryRow("SELECT count(*), CAST(sum(hash("+cols+")) AS VARCHAR) FROM src.crm.customers").Scan(&n, &sum); err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	hashString(h, "crm.customers")
	hashString(h, strconv.FormatInt(n, 10))
	hashString(h, sum)
	return hex.EncodeToString(h.Sum(nil))
}

// cellEdges returns the inner correlation edges of the people group as
// text, those with at least 5 characters (shorter ones match by chance).
func cellEdges(t *testing.T) []string {
	t.Helper()
	db := srcDB(t)
	probs := make([]float64, 19)
	for i := range probs {
		probs[i] = float64(i+1) / 20
	}
	var out []string
	for _, c := range []string{"age", "income", "risk"} {
		var list any
		if err := db.QueryRow("SELECT quantile_cont(CAST(" + c + " AS DOUBLE), " + floatList(probs) + ") FROM src.crm.people").Scan(&list); err != nil {
			t.Fatal(err)
		}
		qs, err := floats(list)
		if err != nil {
			t.Fatal(err)
		}
		for j, q := range qs {
			// The edge at 1/2 is the median, which the report shows.
			if s := strconv.FormatFloat(q, 'g', -1, 64); len(s) >= 5 && j != 9 {
				out = append(out, s)
			}
		}
	}
	return out
}

// Test 18.
func TestNoiseLeaks(t *testing.T) {
	fp := fingerprintOf(t)
	edges := cellEdges(t)
	if len(edges) == 0 {
		t.Fatal("no edge to search")
	}
	for _, tc := range []struct {
		name string
		spec *apitypes.SyntheticSpec
		fail bool
	}{
		{"correlated", peopleSpec(), false},
		{"noise", noiseSpec(), false},
		{"small epsilon", smallSpec(map[string]apitypes.SyntheticColumn{"score": {Generator: apitypes.GenDistribution}}), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			o := opts{dir: t.TempDir(), log: slog.New(slog.NewTextHandler(&buf, debugNoTime))}
			var rep *Report
			var err error
			seed := testSeed
			if tc.fail {
				rep, seed, err = runSmall(t, tc.spec, true, o)
			} else {
				o.spec = tc.spec
				rep, err = run(t, o)
			}
			if (err != nil) != tc.fail {
				t.Fatalf("err = %v", err)
			}
			report, rerr := os.ReadFile(Layout{Dir: o.dir, Name: "crmsynth"}.Report(rep.BuildID))
			if rerr != nil {
				t.Fatal(rerr)
			}
			// The timestamps' digits could match an edge by chance.
			text := buf.String() + string(report)
			for _, ts := range []time.Time{rep.StartedAt, rep.FinishedAt} {
				text = strings.ReplaceAll(text, ts.Format(time.RFC3339Nano), "")
			}
			if err != nil {
				text += err.Error()
			}
			for _, s := range []string{seed, hex.EncodeToString([]byte(seed)), fp, fp[:16]} {
				if strings.Contains(text, s) {
					t.Errorf("seed or fingerprint %q found", s[:8])
				}
			}
			for _, e := range edges {
				if strings.Contains(text, e) {
					t.Errorf("cell edge %s found", e)
				}
			}
		})
	}
}
