// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/bino-bi/sluice/internal/synth/rng"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

var testSeed = []byte("profile-test-seed-0123456789")

func jitterStream(i int) *rng.Stream {
	return rng.For(testSeed, "syn", "crm.t", fmt.Sprintf("c%d", i), rng.PurposeJitter)
}

// TestJitterBounds is the filter part of plan test 3: each bound moves
// by a share of the range in [min, max], up or down.
func TestJitterBounds(t *testing.T) {
	s := Effective()
	const lo, hi = 10.0, 1010.0
	r := hi - lo
	var down, up int
	for i := range 2000 {
		lo2, hi2 := jitterBounds(lo, hi, 0, s, jitterStream(i))
		dl, dh := math.Abs(lo2-lo), math.Abs(hi2-hi)
		if dl < 0.01*r-1e-9 || dl > 0.02*r+1e-9 || dh < 0.01*r-1e-9 || dh > 0.02*r+1e-9 {
			t.Fatalf("draw %d: lo' %v, hi' %v; moves %v and %v not in [%v, %v]", i, lo2, hi2, dl, dh, 0.01*r, 0.02*r)
		}
		if lo2 < lo {
			down++
		} else {
			up++
		}
	}
	if down < 800 || up < 800 {
		t.Errorf("signs not balanced: %d down, %d up", down, up)
	}
	a1, b1 := jitterBounds(lo, hi, 0, s, jitterStream(7))
	a2, b2 := jitterBounds(lo, hi, 0, s, jitterStream(7))
	if a1 != a2 || b1 != b2 {
		t.Error("jitter is not deterministic for one stream")
	}
}

func TestJitterBoundsEdges(t *testing.T) {
	s := Effective()
	if lo, hi := jitterBounds(5, 5, 1, s, jitterStream(0)); lo != 5 || hi != 5 {
		t.Errorf("zero range moved: %v %v", lo, hi)
	}
	off := s
	off.JitterOff = true
	if lo, hi := jitterBounds(0, 100, 1, off, jitterStream(0)); lo != 0 || hi != 100 {
		t.Errorf("jitter off moved: %v %v", lo, hi)
	}
	// Integer column with a small range: the move is at least one unit.
	for i := range 200 {
		lo, hi := jitterBounds(0, 10, 1, s, jitterStream(i))
		if math.Abs(lo) < 1 || math.Abs(hi-10) < 1 {
			t.Fatalf("draw %d: move below one unit: %v %v", i, lo, hi)
		}
		if lo >= hi {
			t.Fatalf("draw %d: empty range %v %v", i, lo, hi)
		}
	}
	// Half-range moves can meet in the middle; the flip keeps lo' < hi'.
	big := s
	big.JitterMin, big.JitterMax = 0.5, 0.5
	for i := range 200 {
		if lo, hi := jitterBounds(0, 1, 0, big, jitterStream(i)); lo >= hi {
			t.Fatalf("draw %d: empty range %v %v", i, lo, hi)
		}
	}
}

func TestMapLinear(t *testing.T) {
	if got := mapLinear(5, 0, 10, 100, 200); got != 150 {
		t.Errorf("mapLinear = %v", got)
	}
	if got := mapLinear(0, 0, 10, -1, 11); got != -1 {
		t.Errorf("mapLinear lo = %v", got)
	}
	if got := mapLinear(10, 0, 10, -1, 11); got != 11 {
		t.Errorf("mapLinear hi = %v", got)
	}
}

func TestMergeOrdinal(t *testing.T) {
	pt := func(v float64, c int64) Bin { return Bin{Lo: v, Hi: v, Count: c} }
	cases := []struct {
		name   string
		in     []Bin
		want   []Bin
		merges int
	}{
		{"all kept", []Bin{pt(1, 5), pt(2, 9)}, []Bin{pt(1, 5), pt(2, 9)}, 0},
		{"to larger neighbor", []Bin{pt(1, 5), pt(2, 1), pt(3, 9)}, []Bin{pt(1, 5), pt(3, 10)}, 1},
		{"tie goes low", []Bin{pt(1, 6), pt(2, 1), pt(3, 6)}, []Bin{pt(1, 7), pt(3, 6)}, 1},
		{"edge", []Bin{pt(1, 2), pt(2, 7)}, []Bin{pt(2, 9)}, 1},
		{"chain", []Bin{pt(1, 1), pt(2, 1), pt(3, 1), pt(4, 2)}, []Bin{pt(2, 5)}, 3},
		{"single below k stays", []Bin{pt(1, 2)}, []Bin{pt(1, 2)}, 0},
		{"empty bins vanish", []Bin{{0, 1, 6}, {1, 2, 0}, {2, 3, 0}, {3, 4, 7}}, []Bin{{0, 1, 6}, {3, 4, 7}}, 2},
	}
	for _, c := range cases {
		got, n := mergeOrdinal(c.in, 5)
		if !reflect.DeepEqual(got, c.want) || n != c.merges {
			t.Errorf("%s: got %v (%d merges), want %v (%d)", c.name, got, n, c.want, c.merges)
		}
	}
}

func TestNullRate(t *testing.T) {
	cases := []struct {
		c    Counts
		want float64
	}{
		{Counts{Rows: 100, NonNull: 97}, 0}, // 3 nulls < k
		{Counts{Rows: 100, NonNull: 3}, 1},  // 3 values < k
		{Counts{Rows: 100, NonNull: 80}, 0.2},
		{Counts{Rows: 0}, 0},
	}
	for _, c := range cases {
		if got := nullRate(c.c, 5); got != c.want {
			t.Errorf("nullRate(%+v) = %v, want %v", c.c, got, c.want)
		}
	}
}

func TestSpikesOf(t *testing.T) {
	got := spikesOf(0, 2, 5)
	if !reflect.DeepEqual(got, []Spike{{Side: SideHigh, Rows: 2}}) {
		t.Errorf("spikesOf = %v", got)
	}
	if got := spikesOf(5, 9, 5); got != nil {
		t.Errorf("no spike expected, got %v", got)
	}
	if got := spikesOf(1, 1, 5); len(got) != 2 {
		t.Errorf("two spikes expected, got %v", got)
	}
}

func TestGridSteps(t *testing.T) {
	for _, c := range []struct {
		n, k int64
		want int
	}{{9, 5, 1}, {10, 5, 2}, {499, 5, 99}, {20000, 5, 100}} {
		if got := gridSteps(c.n, c.k); got != c.want {
			t.Errorf("gridSteps(%d, %d) = %d, want %d", c.n, c.k, got, c.want)
		}
	}
}

func TestEffective(t *testing.T) {
	ten, three := 10, 3
	off := false
	src := &apitypes.SyntheticPrivacy{
		MinGroupSize:  &three,
		ExtremeJitter: &apitypes.ExtremeJitter{Min: 0.05, Max: 0.1},
		SpikeWarning:  &off,
	}
	table := &apitypes.SyntheticPrivacy{MinGroupSize: &ten}
	col := &apitypes.SyntheticPrivacy{ClipOutliers: &apitypes.ClipOutliers{Lower: 0.01, Upper: 0.99}}
	got := Effective(col, table, nil, src)
	want := Settings{
		MinGroupSize: 10,
		Clip:         &apitypes.ClipOutliers{Lower: 0.01, Upper: 0.99},
		JitterMin:    0.05,
		JitterMax:    0.1,
		SpikeWarning: false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Effective = %+v, want %+v", got, want)
	}
	def := Effective()
	if def.MinGroupSize != 5 || def.Clip != nil || def.JitterOff || def.JitterMin != 0.01 || def.JitterMax != 0.02 || !def.SpikeWarning {
		t.Errorf("defaults = %+v", def)
	}
	if !Effective(&apitypes.SyntheticPrivacy{ExtremeJitter: &apitypes.ExtremeJitter{Off: true}}).JitterOff {
		t.Error("extremeJitter off not taken")
	}
}

func TestKindOf(t *testing.T) {
	for typ, want := range map[string]Kind{
		"INTEGER": KindInteger, "UHUGEINT": KindInteger, "DECIMAL(12,2)": KindFloat, "FLOAT": KindFloat,
		"VARCHAR": KindText, "ENUM('M', 'W')": KindEnum, "BOOLEAN": KindBool, "DATE": KindDate,
		"TIMESTAMP WITH TIME ZONE": KindTimestamp, "TIMESTAMP_NS": KindTimestamp, "TIME": KindTime,
		"TIME WITH TIME ZONE": KindOther, "UUID": KindUUID, "INTEGER[]": KindOther, "STRUCT(x INTEGER)": KindOther,
		"BLOB": KindOther, "JSON": KindOther,
	} {
		if got := KindOf(typ); got != want {
			t.Errorf("KindOf(%q) = %v, want %v", typ, got, want)
		}
	}
}

func TestDefaultUnit(t *testing.T) {
	for typ, want := range map[string]float64{
		"INTEGER": 1, "DATE": 1, "DOUBLE": 0, "TIMESTAMP": 0,
		"DECIMAL(2,1)": 0.1, "DECIMAL(12,2)": 0.01, "DECIMAL(18,0)": 1,
	} {
		if got := defaultUnit(typ); math.Abs(got-want) > 1e-12 {
			t.Errorf("defaultUnit(%q) = %v, want %v", typ, got, want)
		}
	}
}
