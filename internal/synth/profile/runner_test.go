// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"reflect"
	"strings"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

const fixtureRows = 2000

// fixture builds src.crm.t with fixed formulas. Rare groups (k = 5):
// gender X (3 rows), revenue two far values, name 'Zq' (2 rows, the only
// length-2 and Z-initial value), code 'at-12x' (3 rows, own pattern),
// notes with 9 words (2 rows). valid_to has one DATE 'infinity' row.
const fixture = `
ATTACH ':memory:' AS src;
CREATE SCHEMA src.crm;
CREATE TYPE src.crm.g AS ENUM ('M', 'W', 'D', 'X');
CREATE TABLE src.crm.t AS
SELECT
  i AS id,
  CAST(CASE WHEN i < 3 THEN 'X' WHEN i < 103 THEN 'D' WHEN i % 2 = 0 THEN 'M' ELSE 'W' END AS src.crm.g) AS gender,
  CAST(CASE WHEN i = 0 THEN 1000000 WHEN i = 1 THEN 999000 ELSE (i % 997) * 1.25 + 10 END AS DECIMAL(12,2)) AS revenue,
  CASE WHEN i < 2 THEN 'Zq' ELSE ['Anna', 'Bernd', 'carla', 'DIETER'][i % 4 + 1] END AS name,
  CASE WHEN i < 3 THEN 'at-12x' ELSE 'DE-' || lpad(CAST(i % 100 AS VARCHAR), 4, '0') END AS code,
  CASE WHEN i < 2 THEN 'a b c d e f g h i' ELSE ['one', 'one two', 'one two three'][i % 3 + 1] END AS notes,
  DATE '2020-01-01' + CAST(i % 365 AS INTEGER) AS day,
  TIMESTAMPTZ '2020-01-01 23:30:00+00' + i * INTERVAL 1 DAY AS ts,
  TIME '08:00:00' + i * INTERVAL 7 SECOND AS opened,
  CASE WHEN i % 10 = 0 THEN NULL ELSE CAST(i AS DOUBLE) END AS maybe,
  CAST(i - 1000 AS DOUBLE) AS neg,
  CASE WHEN i < 3 THEN 1.5 ELSE NULL END AS sparse,
  [i] AS list,
  CAST(1 + (i % 41) / 10 AS DECIMAL(2,1)) AS rating,
  CASE WHEN i = 0 THEN DATE 'infinity' ELSE DATE '2020-01-01' + CAST(i % 365 AS INTEGER) END AS valid_to
FROM range(2000) r(i);
CREATE TABLE src.crm.people AS ` + peopleSelect + `;
SET TimeZone = 'UTC';
`

// peopleSelect is crm.people of plan 11: income rises with age, risk
// falls with it, noise_col is independent. tier gold has 2 rows, bonus
// one far value.
const peopleSelect = `SELECT
  CAST(20 + (i * 7919) % 50 AS INTEGER) AS age,
  CAST(1000 * (20 + (i * 7919) % 50) + (i * 104729) % 20000 AS DOUBLE) AS income,
  CAST(100 - (20 + (i * 7919) % 50) + (i * 31) % 10 AS DOUBLE) AS risk,
  CAST((i * 48271) % 10007 AS DOUBLE) AS noise_col,
  DATE '2000-01-01' + CAST((20 + (i * 7919) % 50) * 30 + i % 30 AS INTEGER) AS joined,
  CASE WHEN i < 2 THEN 'gold' WHEN i < 4002 THEN 'silver' ELSE 'bronze' END AS tier,
  CAST(CASE WHEN i = 0 THEN 990000 ELSE (i * 7919) % 5000 END AS DOUBLE) AS bonus
FROM range(20000) r(i)`

func newRunner(t *testing.T) (*Runner, *bytes.Buffer) {
	t.Helper()
	c, err := duckdb.NewConnector("", nil)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	t.Cleanup(func() { _ = db.Close() })
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.ExecContext(context.Background(), fixture); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return &Runner{Conn: conn, Log: log}, &buf
}

var tbl = Table{Catalog: "src", Schema: "crm", Name: "t"}

func columns(t *testing.T, r *Runner) []ColumnInfo {
	t.Helper()
	rows, err := r.Conn.QueryContext(context.Background(),
		"SELECT column_name, data_type FROM information_schema.columns WHERE table_catalog = 'src' AND table_schema = 'crm' AND table_name = 't' ORDER BY ordinal_position")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []ColumnInfo
	for rows.Next() {
		var c ColumnInfo
		if err := rows.Scan(&c.Name, &c.Type); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

// profileOf runs Base and Column for one column.
func profileOf(t *testing.T, r *Runner, name string, needs Needs, s Settings) (*Column, error) {
	t.Helper()
	cols := columns(t, r)
	counts, err := r.Base(context.Background(), tbl, cols)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range cols {
		if c.Name == name {
			return r.Column(context.Background(), tbl, c, counts[i], needs, s, jitterStream(i))
		}
	}
	t.Fatalf("no column %s", name)
	return nil, nil
}

func mustProfile(t *testing.T, r *Runner, name string, needs Needs, s Settings) *Column {
	t.Helper()
	c, err := profileOf(t, r, name, needs, s)
	if err != nil {
		t.Fatalf("profile %s: %v", name, err)
	}
	return c
}

func scalar[T any](t *testing.T, r *Runner, q string) T {
	t.Helper()
	var v T
	if err := r.Conn.QueryRowContext(context.Background(), q).Scan(&v); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return v
}

func TestBase(t *testing.T) {
	r, _ := newRunner(t)
	cols := columns(t, r)
	counts, err := r.Base(context.Background(), tbl, cols)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Counts{}
	for i, c := range cols {
		byName[c.Name] = counts[i]
	}
	if c := byName["gender"]; c != (Counts{Rows: fixtureRows, NonNull: fixtureRows, Distinct: 4}) {
		t.Errorf("gender counts %+v", c)
	}
	if c := byName["maybe"]; c.NonNull != 1800 || c.Distinct != 1800 {
		t.Errorf("maybe counts %+v", c)
	}
	if c := byName["list"]; c.NonNull != fixtureRows || c.Distinct != 0 {
		t.Errorf("list counts %+v", c)
	}
}

func TestNumberProfile(t *testing.T) {
	r, _ := newRunner(t)
	s := Effective()
	all := Needs{Parts: PartRange | PartStats | PartQuantiles | PartBins, Bins: 20}
	c := mustProfile(t, r, "revenue", all, s)

	lo, hi := 10.0, 1000000.0
	rng := hi - lo
	if d := math.Abs(c.Lo - lo); d < 0.01*rng || d > 0.02*rng {
		t.Errorf("lo' = %v moved %v, want [%v, %v]", c.Lo, d, 0.01*rng, 0.02*rng)
	}
	if d := math.Abs(c.Hi - hi); d < 0.01*rng || d > 0.02*rng {
		t.Errorf("hi' = %v moved %v", c.Hi, d)
	}
	if c.InRange != fixtureRows {
		t.Errorf("InRange = %d", c.InRange)
	}
	if len(c.Quantiles) != 101 || c.Quantiles[0] != c.Lo || c.Quantiles[100] != c.Hi {
		t.Errorf("quantiles: %d entries, ends %v %v", len(c.Quantiles), c.Quantiles[0], c.Quantiles[len(c.Quantiles)-1])
	}
	for i := 1; i < len(c.Quantiles); i++ {
		if c.Quantiles[i] < c.Quantiles[i-1] {
			t.Fatalf("quantiles not sorted at %d", i)
		}
	}
	wantMean := scalar[float64](t, r, "SELECT avg(CAST(revenue AS DOUBLE)) FROM src.crm.t")
	if math.Abs(c.Mean-wantMean) > 1e-6*wantMean {
		t.Errorf("mean = %v, want %v", c.Mean, wantMean)
	}
	// The two far values sit alone at the top: a spike. The bins between
	// them and the bulk are empty and merged away.
	if !reflect.DeepEqual(c.Spikes, []Spike{{Side: SideHigh, Rows: 2}}) {
		t.Errorf("spikes = %v", c.Spikes)
	}
	var total int64
	for _, b := range c.Bins {
		if b.Count < 5 {
			t.Errorf("bin below k: %+v", b)
		}
		if b.Lo < c.Lo || b.Hi > c.Hi {
			t.Errorf("bin outside jittered range: %+v", b)
		}
		total += b.Count
	}
	if total != fixtureRows || c.MergedBins == 0 {
		t.Errorf("bins: total %d, merges %d", total, c.MergedBins)
	}

	// With clipOutliers the far values are cut: no spike, bounds near
	// the clip quantiles.
	s.Clip = &apitypes.ClipOutliers{Lower: 0.01, Upper: 0.99}
	c = mustProfile(t, r, "revenue", all, s)
	if len(c.Spikes) != 0 {
		t.Errorf("spikes with clip = %v", c.Spikes)
	}
	if c.Hi > 2000 || c.InRange >= fixtureRows {
		t.Errorf("clip not applied: hi' %v, in range %d", c.Hi, c.InRange)
	}

	s = Effective()
	s.SpikeWarning = false
	if c := mustProfile(t, r, "revenue", Needs{Parts: PartRange}, s); c.Spikes != nil {
		t.Errorf("spikeWarning off still found %v", c.Spikes)
	}
}

func TestProfileIsDeterministic(t *testing.T) {
	r, _ := newRunner(t)
	needs := Needs{Parts: PartRange | PartStats | PartQuantiles | PartBins | PartLogStats, Bins: 10}
	a := mustProfile(t, r, "revenue", needs, Effective())
	b := mustProfile(t, r, "revenue", needs, Effective())
	if !reflect.DeepEqual(a, b) {
		t.Error("two profiles of the same column differ")
	}
}

func TestOtherAxes(t *testing.T) {
	r, _ := newRunner(t)
	s := Effective()
	s.JitterOff = true
	needs := Needs{Parts: PartRange | PartQuantiles}
	day := mustProfile(t, r, "day", needs, s)
	if day.Lo != 18262 || day.Hi != 18262+364 {
		t.Errorf("day axis %v %v", day.Lo, day.Hi)
	}
	opened := mustProfile(t, r, "opened", needs, s)
	if opened.Lo != 8*3600 || opened.Hi != 8*3600+7*1999 {
		t.Errorf("time axis %v %v", opened.Lo, opened.Hi)
	}
	ts := mustProfile(t, r, "ts", needs, s)
	if ts.Lo != 1577921400 {
		t.Errorf("timestamp axis lo %v", ts.Lo)
	}
	// The infinity row is not on the axis.
	if v := mustProfile(t, r, "valid_to", needs, s); v.Lo != 18262 || v.Hi != 18262+364 || v.InRange != fixtureRows-1 {
		t.Errorf("valid_to axis %v %v, in range %d", v.Lo, v.Hi, v.InRange)
	}
	// Dates move by at least one day.
	j := mustProfile(t, r, "day", needs, Effective())
	if math.Abs(j.Lo-18262) < 1 || math.Abs(j.Hi-18626) < 1 {
		t.Errorf("date jitter below one day: %v %v", j.Lo, j.Hi)
	}
}

func TestLogStats(t *testing.T) {
	r, buf := newRunner(t)
	c := mustProfile(t, r, "maybe", Needs{Parts: PartLogStats}, Effective())
	want := scalar[float64](t, r, "SELECT avg(ln(maybe)) FROM src.crm.t")
	if math.Abs(c.LogMean-want) > 1e-9 || c.LogStdDev <= 0 {
		t.Errorf("log stats %v %v, want mean %v", c.LogMean, c.LogStdDev, want)
	}
	_, err := profileOf(t, r, "neg", Needs{Parts: PartLogStats}, Effective())
	if !errors.Is(err, ErrNotPositive) {
		t.Errorf("negative values: %v", err)
	}
	if strings.Contains(err.Error(), "1000") || strings.Contains(buf.String(), "-1000") {
		t.Errorf("error or log carries a value: %v", err)
	}
}

func TestTopValues(t *testing.T) {
	r, _ := newRunner(t)
	c := mustProfile(t, r, "gender", Needs{Parts: PartTopValues, MaxValues: 50}, Effective())
	want := []Value{{"W", 949}, {"M", 948}, {"D", 100}}
	if !reflect.DeepEqual(c.TopValues.Values, want) || c.TopValues.Rest != 3 || c.Suppressed != 1 {
		t.Errorf("top values %+v, suppressed %d", c.TopValues, c.Suppressed)
	}
	c = mustProfile(t, r, "gender", Needs{Parts: PartTopValues, MaxValues: 1}, Effective())
	if len(c.TopValues.Values) != 1 || c.TopValues.Rest != fixtureRows-949 || c.Suppressed != 1 {
		t.Errorf("maxValues 1: %+v, suppressed %d", c.TopValues, c.Suppressed)
	}
	if _, err := profileOf(t, r, "sparse", Needs{Parts: PartTopValues}, Effective()); !errors.Is(err, ErrTooFewRows) {
		t.Errorf("3 non-null rows: %v", err)
	}
	if _, err := profileOf(t, r, "id", Needs{Parts: PartTopValues}, Effective()); !errors.Is(err, ErrNoGroups) {
		t.Errorf("all groups below k: %v", err)
	}
}

func TestTextProfile(t *testing.T) {
	r, _ := newRunner(t)
	all := Needs{Parts: PartLengths | PartInitials | PartCharClasses | PartCaseStyles | PartPrefixes, PrefixLength: 1}
	c := mustProfile(t, r, "name", all, Effective())
	// 'Zq' (length 2) merges into length 4 ('Anna'), the only neighbor.
	wantLengths := []Bin{{4, 4, 501, 0}, {5, 5, 999, 0}, {6, 6, 500, 0}}
	if !reflect.DeepEqual(c.Lengths, wantLengths) || c.MergedBins != 1 {
		t.Errorf("lengths %v, merges %d", c.Lengths, c.MergedBins)
	}
	for _, v := range c.Initials.Values {
		if v.V == "Z" {
			t.Error("rare initial Z kept")
		}
	}
	if len(c.Initials.Values) != 4 || c.Initials.Rest != 2 {
		t.Errorf("initials %+v", c.Initials)
	}
	if c.CharClasses != (CharClasses{Upper: true, Lower: true}) {
		t.Errorf("char classes %+v", c.CharClasses)
	}
	// Anna, Bernd title; carla lower; DIETER upper; Zq title too.
	if c.CaseStyles != [4]int64{500, 500, 1000, 0} {
		t.Errorf("case styles %v", c.CaseStyles)
	}
	if c.Suppressed != 2 {
		t.Errorf("suppressed %d, want initial Z and prefix Z", c.Suppressed)
	}

	code := mustProfile(t, r, "code", Needs{Parts: PartPatterns | PartPrefixes, MaxPatterns: 20, PrefixLength: 3}, Effective())
	if !reflect.DeepEqual(code.Patterns.Values, []Value{{"AA-9999", 1997}}) || code.Patterns.Rest != 3 {
		t.Errorf("patterns %+v", code.Patterns)
	}
	if !reflect.DeepEqual(code.Prefixes.Values, []Value{{"DE-", 1997}}) {
		t.Errorf("prefixes %+v", code.Prefixes)
	}

	notes := mustProfile(t, r, "notes", Needs{Parts: PartWordCounts}, Effective())
	var words []float64
	for _, b := range notes.WordCounts {
		words = append(words, b.Lo)
	}
	if !reflect.DeepEqual(words, []float64{1, 2, 3}) {
		t.Errorf("word counts %v", notes.WordCounts)
	}
}

// TestPatternKeepsCase is the profile half of plan test 15: 'De-47x'
// gives 'Aa-99a'.
func TestPatternKeepsCase(t *testing.T) {
	r, _ := newRunner(t)
	got := scalar[string](t, r, "SELECT "+patternExpr("'De-47xÆǅ'"))
	if got != "Aa-99aAa" {
		t.Errorf("pattern = %q", got)
	}
}

func TestDateParts(t *testing.T) {
	r, _ := newRunner(t)
	needs := Needs{Parts: PartWeekdays | PartMonths}
	day := mustProfile(t, r, "day", needs, Effective())
	if len(day.Weekdays.Values) != 7 || len(day.Months.Values) != 12 {
		t.Errorf("day: %d weekdays, %d months", len(day.Weekdays.Values), len(day.Months.Values))
	}
	// 2020-01-01 23:30 UTC is a Wednesday (3). Under a zone east of UTC
	// the first row would be a Thursday, so the counts would shift.
	ts := mustProfile(t, r, "ts", needs, Effective())
	want := int64(scalar[int64](t, r, "SELECT count(*) FROM src.crm.t WHERE dayofweek(ts AT TIME ZONE 'UTC') = 3"))
	for _, v := range ts.Weekdays.Values {
		if v.V == int64(3) && v.Count != want {
			t.Errorf("Wednesday count %d, want %d", v.Count, want)
		}
	}
	if _, err := profileOf(t, r, "opened", needs, Effective()); !errors.Is(err, ErrKind) {
		t.Errorf("weekdays on TIME: %v", err)
	}
}

func TestProfileErrors(t *testing.T) {
	r, buf := newRunner(t)
	if _, err := profileOf(t, r, "name", Needs{Parts: PartQuantiles}, Effective()); !errors.Is(err, ErrKind) {
		t.Errorf("quantiles on VARCHAR: %v", err)
	}
	if _, err := profileOf(t, r, "revenue", Needs{Parts: PartLengths}, Effective()); !errors.Is(err, ErrKind) {
		t.Errorf("lengths on DECIMAL: %v", err)
	}
	if _, err := profileOf(t, r, "list", Needs{Parts: PartTopValues}, Effective()); !errors.Is(err, ErrKind) {
		t.Errorf("top values on a list: %v", err)
	}
	if _, err := profileOf(t, r, "sparse", Needs{Parts: PartRange}, Effective()); !errors.Is(err, ErrTooFewRows) {
		t.Errorf("3 rows: %v", err)
	}
	s := Effective()
	s.MinGroupSize = 1001
	if _, err := profileOf(t, r, "revenue", Needs{Parts: PartQuantiles}, s); !errors.Is(err, ErrGrid) {
		t.Errorf("grid of 2000 rows with k 1001: %v", err)
	}
	// Profile-free generators run no query and pass with few rows.
	buf.Reset()
	c, err := profileOf(t, r, "sparse", Needs{}, Effective())
	if err != nil || c.NullRate != 1 {
		t.Errorf("no needs: %v, null rate %v", err, c.NullRate)
	}
	_, err = profileOf(t, r, "name", Needs{Parts: PartPrefixes, PrefixLength: 40}, Effective())
	if !errors.Is(err, ErrNoGroups) {
		t.Errorf("prefix longer than every value: %v", err)
	}
	if strings.Contains(err.Error(), "Anna") {
		t.Errorf("error carries a value: %v", err)
	}
}

func TestQueryErrorHidesDuckDBText(t *testing.T) {
	r, buf := newRunner(t)
	_, err := r.Column(context.Background(), Table{Catalog: "src", Schema: "crm", Name: "missing"},
		ColumnInfo{Name: "x", Type: "DOUBLE"}, Counts{Rows: 10, NonNull: 10}, Needs{Parts: PartRange}, Effective(), jitterStream(0))
	if !errors.Is(err, ErrQuery) || err.Error() != "column x: profile query failed" {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(buf.String(), "missing") {
		t.Errorf("raw DuckDB error not in the debug log: %s", buf.String())
	}
}

// TestOrderedAggregatesAreStable is the step B probe of plan 1.7: plain
// sums of DOUBLE values depend on the thread schedule, ordered
// aggregates must not.
func TestOrderedAggregatesAreStable(t *testing.T) {
	c, err := duckdb.NewConnector("", nil)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "CREATE TABLE d AS SELECT sin(i) * 1e6 + i * 0.1 + 1 / (i + 1) AS x FROM range(1000000) r(i)"); err != nil {
		t.Fatal(err)
	}
	for _, agg := range []string{
		"avg(x ORDER BY x)",
		"stddev_samp(x ORDER BY x)",
		"avg(ln(abs(x) + 1) ORDER BY ln(abs(x) + 1))",
		"stddev_samp(ln(abs(x) + 1) ORDER BY ln(abs(x) + 1))",
	} {
		var bits []uint64
		for _, threads := range []int{1, 4, 4, 8} {
			conn, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var v float64
			if _, err := conn.ExecContext(ctx, fmt.Sprintf("SET threads = %d", threads)); err != nil {
				t.Fatal(err)
			}
			if err := conn.QueryRowContext(ctx, "SELECT "+agg+" FROM d").Scan(&v); err != nil {
				t.Fatal(err)
			}
			_ = conn.Close()
			bits = append(bits, math.Float64bits(v))
		}
		for _, b := range bits[1:] {
			if b != bits[0] {
				t.Errorf("%s differs between thread counts: %x", agg, bits)
				break
			}
		}
	}
}

// TestTimeZoneInAttachHook is the step B probe of plan 1.3: the builder
// sets TimeZone in its attach hook, before the executor locks the
// configuration, and ICU is built in, so TIMESTAMPTZ parts use UTC.
func TestTimeZoneInAttachHook(t *testing.T) {
	t.Setenv("TZ", "Asia/Tokyo")
	ctx := context.Background()
	e, err := executor.New(ctx, executor.Config{
		MaxOpen: 1,
		AttachHook: func(ctx context.Context, conn *sql.Conn) error {
			_, err := conn.ExecContext(ctx, "SET TimeZone = 'UTC'")
			return err
		},
	})
	if err != nil {
		t.Fatalf("executor with TimeZone hook: %v", err)
	}
	defer func() { _ = e.Close() }()
	one := func(q string) (any, error) {
		resp, err := e.Execute(ctx, executor.Request{SQL: q})
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Rows.Close() }()
		var v any
		if !resp.Rows.Next() {
			return nil, resp.Rows.Err()
		}
		err = resp.Rows.Scan(&v)
		return v, err
	}
	if v, err := one("SELECT current_setting('TimeZone')"); err != nil || v != "UTC" {
		t.Errorf("TimeZone = %v, %v", v, err)
	}
	// 23:30 UTC on a Wednesday is Thursday in Tokyo.
	if v, err := one("SELECT dayofweek(TIMESTAMPTZ '2020-01-01 23:30:00+00')"); err != nil || v != int64(3) {
		t.Errorf("dayofweek = %v, %v; want 3", v, err)
	}
	if v, err := one("SELECT month(TIMESTAMPTZ '2020-01-31 23:30:00+00')"); err != nil || v != int64(1) {
		t.Errorf("month = %v, %v; want 1", v, err)
	}
	if _, err := one("SET TimeZone = 'Asia/Tokyo'"); err == nil {
		t.Error("TimeZone changed after the lock")
	}
}

// A DECIMAL(p,s) bound moves by at least 10^-s, so it never rounds back
// to the source bound when the snapshot casts it to the column type.
func TestDecimalJitterUnit(t *testing.T) {
	r, _ := newRunner(t)
	cols := columns(t, r)
	counts, err := r.Base(context.Background(), tbl, cols)
	if err != nil {
		t.Fatal(err)
	}
	var i int
	for i = range cols {
		if cols[i].Name == "rating" {
			break
		}
	}
	for seed := range 200 {
		c, err := r.Column(context.Background(), tbl, cols[i], counts[i], Needs{Parts: PartRange}, Effective(), jitterStream(seed))
		if err != nil {
			t.Fatal(err)
		}
		if math.Round(c.Lo*10) == 10 || math.Round(c.Hi*10) == 50 {
			t.Fatalf("seed %d: bounds %v %v round to the source bounds 1.0 5.0", seed, c.Lo, c.Hi)
		}
	}
}

// A generator unit below the type step does not replace it: on a
// DECIMAL(2,1) column a move of 0.01 would round back to the source bound.
func TestJitterUnitFloor(t *testing.T) {
	r, _ := newRunner(t)
	cols := columns(t, r)
	counts, err := r.Base(context.Background(), tbl, cols)
	if err != nil {
		t.Fatal(err)
	}
	var i int
	for i = range cols {
		if cols[i].Name == "rating" {
			break
		}
	}
	for seed := range 200 {
		c, err := r.Column(context.Background(), tbl, cols[i], counts[i], Needs{Parts: PartRange, Unit: 0.01}, Effective(), jitterStream(seed))
		if err != nil {
			t.Fatal(err)
		}
		if math.Round(c.Lo*10) == 10 || math.Round(c.Hi*10) == 50 {
			t.Fatalf("seed %d: bounds %v %v round to the source bounds 1.0 5.0", seed, c.Lo, c.Hi)
		}
	}
}
