// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"context"
	"errors"
	"math"
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/synth/rng"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// testNoise returns the noise of one column with streams keyed by seed.
func testNoise(eps float64, seed, column string) *Noise {
	return &Noise{Epsilon: eps, Stream: func(m string) *rng.Stream {
		return rng.For([]byte(seed), "n", "t", column, m)
	}}
}

func noiseFunc(eps float64, seed string) func(string) *Noise {
	return func(column string) *Noise { return testNoise(eps, seed, column) }
}

func TestMeasurements(t *testing.T) {
	for _, c := range []struct {
		parts Part
		want  int
		ok    bool
	}{
		{0, 0, true},
		{PartRange, 0, true},
		{PartQuantiles, 1, true},
		{PartRange | PartStats | PartBins, 1, true},
		{PartLengths | PartCaseStyles, 2, true},
		{PartQuantiles | PartWeekdays | PartMonths, 3, true},
		{PartVocabulary, 0, false},
		{PartOffset | PartQuantiles, 0, false},
		{PartFanOut, 0, false},
	} {
		n, ok := Measurements(Needs{Parts: c.parts})
		if n != c.want || ok != c.ok {
			t.Errorf("parts %b: %d, %v, want %d, %v", c.parts, n, ok, c.want, c.ok)
		}
	}
}

func TestValueKey(t *testing.T) {
	ts := time.Date(2024, 1, 2, 3, 4, 5, 6, time.FixedZone("x", 3600))
	for _, c := range []struct {
		v    any
		want string
	}{
		{"a", "s:a"}, {int64(3), "i:3"}, {int32(-3), "i:-3"}, {1.5, "f:1.5"},
		{true, "b:true"}, {ts, "t:2024-01-02T02:04:05.000000006Z"}, {nil, "n:"},
	} {
		if got := valueKey(c.v); got != c.want {
			t.Errorf("valueKey(%v) = %q, want %q", c.v, got, c.want)
		}
	}
}

// Test 11.
func TestNoisyCounts(t *testing.T) {
	cols := []ColumnInfo{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	exact := []Counts{{Rows: 1000, NonNull: 1000, Distinct: 10}, {Rows: 1000, NonNull: 700, Distinct: 5}, {Rows: 1000, NonNull: 0}}
	noisy := NoisyCounts(exact, cols, noiseFunc(0.5, "seed-1"))
	for i, c := range noisy {
		if c.Rows != noisy[0].Rows || c.NonNull < 0 || c.NonNull > c.Rows || c.Distinct != 0 {
			t.Errorf("column %d: %+v", i, c)
		}
	}
	if again := NoisyCounts(exact, cols, noiseFunc(0.5, "seed-1")); !slices.Equal(again, noisy) {
		t.Errorf("same counts give %v and %v", noisy, again)
	}
	changed := slices.Clone(exact)
	changed[1].NonNull = 650
	other := NoisyCounts(changed, cols, noiseFunc(0.5, "seed-1"))
	if other[0] != noisy[0] || other[2] != noisy[2] || other[1] == noisy[1] {
		t.Errorf("a changed null count of b: %v, before %v", other, noisy)
	}
	// Large noise clamps into [0, rows'].
	for i := range 50 {
		for _, c := range NoisyCounts(exact, cols, noiseFunc(0.001, string(rune('a'+i)))) {
			if c.Rows < 0 || c.NonNull < 0 || c.NonNull > c.Rows {
				t.Fatalf("out of range: %+v", c)
			}
		}
	}
}

// peopleColumn profiles one column of src.crm.people.
func peopleColumn(t *testing.T, r *Runner, name string, needs Needs, s Settings, noise *Noise) (*Column, error) {
	t.Helper()
	ctx := context.Background()
	info := ColumnInfo{Name: name, Type: scalar[string](t, r, "SELECT data_type FROM information_schema.columns WHERE table_catalog = 'src' AND table_name = 'people' AND column_name = '"+name+"'")}
	counts, err := r.Base(ctx, people, []ColumnInfo{info})
	if err != nil {
		t.Fatal(err)
	}
	return r.Column(ctx, people, info, counts[0], needs, s, jitterStream(1), noise)
}

func bonusBounds() Settings {
	s := Effective()
	s.Bounds = &apitypes.SyntheticBounds{Min: 0.0, Max: 6000.0}
	return s
}

// Test 12.
func TestNoisyNumbersBounds(t *testing.T) {
	r, _ := newRunner(t)
	c, err := peopleColumn(t, r, "bonus", Needs{Parts: PartQuantiles}, bonusBounds(), testNoise(1, "seed-12", "bonus"))
	if err != nil {
		t.Fatal(err)
	}
	// Bins are 30 wide. 990000 is clamped into the top bin, which holds one
	// row and is dropped; the data below 5000 ends in the bin 4980-5010.
	if c.Hi > 5010 || c.Lo < 0 || math.Mod(c.Lo, 30) != 0 || math.Mod(c.Hi, 30) != 0 {
		t.Errorf("range [%v, %v], want bin edges at or below 5010", c.Lo, c.Hi)
	}
	if c.Quantiles[0] != c.Lo || c.Quantiles[len(c.Quantiles)-1] != c.Hi || len(c.Spikes) != 0 {
		t.Errorf("quantiles %v..%v, spikes %v", c.Quantiles[0], c.Quantiles[len(c.Quantiles)-1], c.Spikes)
	}
	// ks of the noisy grid against the exact values below 5000.
	rows, err := r.Conn.QueryContext(context.Background(), "SELECT bonus FROM src.crm.people WHERE bonus < 5000 ORDER BY 1")
	if err != nil {
		t.Fatal(err)
	}
	var vals []float64
	for rows.Next() {
		var v float64
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		vals = append(vals, v)
	}
	_ = rows.Close()
	var ks float64
	s := len(c.Quantiles) - 1
	for i, q := range c.Quantiles {
		n := sort.SearchFloat64s(vals, math.Nextafter(q, math.Inf(1)))
		ks = math.Max(ks, math.Abs(float64(i)/float64(s)-float64(n)/float64(len(vals))))
	}
	if ks > 0.03 {
		t.Errorf("ks = %.4f", ks)
	}

	// Without bounds the range is the phase 2 range; the noisy one shrinks
	// to the kept bins inside it.
	plain, err := peopleColumn(t, r, "bonus", Needs{Parts: PartQuantiles}, Effective(), nil)
	if err != nil {
		t.Fatal(err)
	}
	noisy, err := peopleColumn(t, r, "bonus", Needs{Parts: PartQuantiles}, Effective(), testNoise(1, "seed-12", "bonus"))
	if err != nil {
		t.Fatal(err)
	}
	if noisy.Lo != plain.Lo || noisy.Hi > plain.Hi || noisy.Hi >= 990000 {
		t.Errorf("without bounds: noisy [%v, %v], phase 2 [%v, %v]", noisy.Lo, noisy.Hi, plain.Lo, plain.Hi)
	}

	// Range only, without bounds: the spike check still runs.
	rng, err := peopleColumn(t, r, "bonus", Needs{Parts: PartRange}, Effective(), testNoise(1, "seed-12", "bonus"))
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := peopleColumn(t, r, "bonus", Needs{Parts: PartRange}, Effective(), nil); len(want.Spikes) == 0 || !reflect.DeepEqual(rng.Spikes, want.Spikes) {
		t.Errorf("range only: spikes %v, phase 2 %v", rng.Spikes, want.Spikes)
	}

	// The value 0 is in the data; the check uses bin midpoints.
	if _, err := peopleColumn(t, r, "bonus", Needs{Parts: PartLogStats}, bonusBounds(), testNoise(1, "seed-12", "bonus")); err != nil {
		t.Errorf("lognormal with bounds: %v", err)
	}

	h, err := peopleColumn(t, r, "bonus", Needs{Parts: PartBins, Bins: 20}, bonusBounds(), testNoise(1, "seed-12", "bonus"))
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Bins) == 0 || len(h.Bins) > 20 {
		t.Fatalf("bins = %v", h.Bins)
	}
	for _, b := range h.Bins {
		if b.Hi-b.Lo != 300 || math.Mod(b.Lo, 300) != 0 {
			t.Errorf("bin %+v is not one of 20 cells on [0, 6000]", b)
		}
	}

	if _, err := peopleColumn(t, r, "tier", Needs{Parts: PartTopValues}, bonusBounds(), testNoise(1, "seed-12", "tier")); err != nil {
		t.Errorf("bounds on a nominal part are not read: %v", err)
	}
	ds := Effective()
	ds.Bounds = &apitypes.SyntheticBounds{Min: 0.0, Max: 1.0}
	if _, err := peopleColumn(t, r, "joined", Needs{Parts: PartQuantiles}, ds, testNoise(1, "seed-12", "joined")); !errors.Is(err, ErrBounds) ||
		err.Error() != "column joined: privacy.bounds does not fit the column type" {
		t.Errorf("number bounds on DATE: err = %v", err)
	}
	ds.Bounds = &apitypes.SyntheticBounds{Min: "2000-01-01", Max: "2005-12-31"}
	j, err := peopleColumn(t, r, "joined", Needs{Parts: PartQuantiles}, ds, testNoise(1, "seed-12", "joined"))
	if err != nil {
		t.Fatal(err)
	}
	if lo, hi := 10957.0, 13148.0; j.Lo < lo || j.Hi > hi {
		t.Errorf("joined range [%v, %v], want inside [%v, %v]", j.Lo, j.Hi, lo, hi)
	}
}

func TestBoundsAxis(t *testing.T) {
	for _, c := range []struct {
		k        Kind
		min, max any
		lo, hi   float64
		ok       bool
	}{
		{KindFloat, 0.0, 10.5, 0, 10.5, true},
		{KindTime, 3600.0, 7200.0, 3600, 7200, true},
		{KindDate, "1969-12-31", "1970-01-02T12:00:00", -1, 1, true},
		{KindTimestamp, "1970-01-01", "1970-01-01T00:00:01.5Z", 0, 1.5, true},
		{KindDate, 0.0, 1.0, 0, 0, false},
		{KindInteger, "2020-01-01", "2021-01-01", 0, 0, false},
		{KindDate, "2020-01-01T10:00:00", "2020-01-01T12:00:00", 0, 0, false},
	} {
		lo, hi, err := boundsAxis(c.k, &apitypes.SyntheticBounds{Min: c.min, Max: c.max})
		if (err == nil) != c.ok || c.ok && (lo != c.lo || hi != c.hi) {
			t.Errorf("%v, %v on kind %d: %v, %v, %v", c.min, c.max, c.k, lo, hi, err)
		}
	}
}

// Test 13.
func TestNoisyNominal(t *testing.T) {
	r, _ := newRunner(t)
	c, err := peopleColumn(t, r, "tier", Needs{Parts: PartTopValues, MaxValues: 10}, Effective(), testNoise(1, "seed-13", "tier"))
	if err != nil {
		t.Fatal(err)
	}
	var total, silver int64
	for _, v := range c.TopValues.Values {
		if v.V == "gold" {
			t.Error("gold (2 rows) appears")
		}
		if v.V == "silver" {
			silver = v.Count
		}
		total += v.Count
	}
	if d := math.Abs(float64(silver)/float64(total+c.TopValues.Rest) - 0.2); d > 0.02 {
		t.Errorf("silver share off by %.4f", d)
	}

	// Nominal labels use k, not tau: at b = 1000 bronze stays.
	c, err = peopleColumn(t, r, "tier", Needs{Parts: PartTopValues, MaxValues: 10}, Effective(), testNoise(0.001, "seed-13", "tier"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(c.TopValues.Values, func(v Value) bool { return v.V == "bronze" }) {
		t.Errorf("bronze dropped: %+v", c.TopValues)
	}

	// The weekday cells are read without a k filter: joined has every
	// weekday, and a filter at k above every count would drop them all.
	p := &colRun{ctx: context.Background(), r: r, from: people.sql(), name: "joined", k: 1 << 40}
	x := parser.QuoteIdent("joined")
	cells, err := p.cellCounts("dayofweek("+x+")", x+" IS NOT NULL", 0, 6)
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, n := range cells {
		if n == 0 {
			t.Errorf("weekday cells %v", cells)
		}
		sum += n
	}
	if sum != 20000 {
		t.Errorf("weekday cells sum to %d", sum)
	}
	w, err := peopleColumn(t, r, "joined", Needs{Parts: PartWeekdays | PartMonths}, Effective(), testNoise(1, "seed-13", "joined"))
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Weekdays.Values) != 7 || len(w.Months.Values) != 12 || w.Weekdays.Rest != 0 {
		t.Errorf("weekdays %+v, months %+v", w.Weekdays, w.Months)
	}
}

// A label with a noisy count in [k, tau) stays, and ties sort by
// valueKey.
func TestNoisyNominalRule(t *testing.T) {
	const k, eps = 5, 0.01 // b = 100, tau of 2 cells = 70
	found := false
	for i := range 200 {
		seed := string(rune('A' + i))
		n := testNoise(eps, seed, "c")
		r := n.Stream("values")
		want := max(0, int64(math.Round(50+r.Laplace(1/eps))))
		if want < k || want >= 70 {
			continue
		}
		p := &colRun{k: k, noise: n}
		got, _ := p.noisyNominal("values", []Value{{V: "a", Count: 50}}, 0)
		if len(got) != 1 || got[0].Count != want {
			t.Fatalf("noisy count %d below tau: got %+v", want, got)
		}
		found = true
		break
	}
	if !found {
		t.Fatal("no stream gives a count in [k, tau)")
	}

	p := &colRun{k: k, noise: testNoise(1e9, "ties", "c")}
	got, rest := p.noisyNominal("values", []Value{{V: "b", Count: 10}, {V: "a", Count: 10}, {V: int64(3), Count: 10}, {V: "c", Count: 20}}, 3)
	var order []any
	for _, v := range got {
		order = append(order, v.V)
	}
	if !slices.Equal(order, []any{"c", int64(3), "a", "b"}) || rest != 0 {
		t.Errorf("order %v, rest %d", order, rest)
	}
}

func TestNoisyCorrelation(t *testing.T) {
	r, _ := newRunner(t)
	infos, edges := peopleEdges(t, r, "age", "income", "noise_col")
	c, err := r.Correlation(context.Background(), people, infos, edges, 5, noiseFunc(1, "seed-c"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Rho[0][1] < 0.85 || math.Abs(c.Rho[0][2]) > 0.05 {
		t.Errorf("rho = %v", c.Rho)
	}
	exact, err := r.Correlation(context.Background(), people, infos, edges, 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	if exact.Rho[0][1] == c.Rho[0][1] {
		t.Error("noise does not change rho")
	}
	// Under heavy noise the cells still give a rho in [-1, 1].
	c, err = r.Correlation(context.Background(), people, infos, edges, 5, noiseFunc(0.0001, "seed-c"))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range c.Rho {
		for _, v := range row {
			if v < -1 || v > 1 || math.IsNaN(v) {
				t.Errorf("rho = %v", c.Rho)
			}
		}
	}
}
