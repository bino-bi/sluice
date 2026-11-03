// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

import (
	"fmt"
	"math"
	"math/rand/v2"
	"runtime"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/internal/synth/rng"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

func TestFanOutSlots(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for range 200 {
		m := 1 + r.IntN(50)
		n := int64(r.IntN(500))
		f := make([]int64, m)
		for i := range f {
			f[i] = int64(r.IntN(8))
		}
		orig := append([]int64(nil), f...)
		slots, scale := fanOutSlots(f, n)
		if int64(len(slots)) != n {
			t.Fatalf("f %v, n %d: %d slots", orig, n, len(slots))
		}
		got := make([]int64, m)
		for _, s := range slots {
			got[s]++
		}
		for i, x := range f {
			// f is set to 1 everywhere when its sum is 0.
			if bound := int64(math.Ceil(float64(x) * scale)); got[i] > bound {
				t.Fatalf("f %v, n %d: parent %d has %d slots, bound %d", orig, n, i, got[i], bound)
			}
		}
	}

	// 3 missing slots, all remainders equal: the lower indexes get them.
	slots, scale := fanOutSlots([]int64{1, 1, 1, 1, 1}, 8)
	if fmt.Sprint(slots) != "[0 0 1 1 2 2 3 4]" || scale != 1.6 {
		t.Errorf("ties: slots %v, scale %v", slots, scale)
	}
	// The largest remainder goes first: 3*7/10 = 2.1, 7*7/10 = 4.9.
	if slots, _ := fanOutSlots([]int64{3, 7}, 7); fmt.Sprint(slots) != "[0 0 1 1 1 1 1]" {
		t.Errorf("remainder: slots %v", slots)
	}
	// A zero sum counts as fan-out 1.
	if slots, scale := fanOutSlots([]int64{0, 0}, 3); fmt.Sprint(slots) != "[0 0 1]" || scale != 1.5 {
		t.Errorf("zero sum: slots %v, scale %v", slots, scale)
	}
	// Large products do not overflow.
	if slots, _ := fanOutSlots([]int64{100000, 100000}, 1<<20); len(slots) != 1<<20 {
		t.Errorf("large: %d slots", len(slots))
	}
}

func fkStreams(col string) Streams {
	s := streams(col)
	f := func(p string) *rng.Stream { return rng.For(testSeed, "syn", "crm.orders", col, p) }
	s.FanOut, s.Orphans = f(rng.PurposeFanOut), f(rng.PurposeOrphans)
	return s
}

func fkCol(typ string, rows int64, keys ...any) Column {
	c := col("customer_id", typ)
	c.Rows = rows
	st := StageBigint
	if len(keys) > 0 {
		if _, ok := keys[0].(string); ok {
			st = StageVarchar
		}
	}
	c.Keys = &KeySet{Values: keys, Stage: st}
	return c
}

func intKeys(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = int64(100 + i)
	}
	return out
}

func drawFK(t *testing.T, cg ColumnGen, rows int) []any {
	t.Helper()
	out := make([]any, rows)
	for i := range out {
		v, err := cg.Next(RowCtx{Index: int64(i)})
		if err != nil {
			t.Fatal(err)
		}
		out[i] = v
	}
	return out
}

func TestForeignKeyCheckNeeds(t *testing.T) {
	g, _ := Lookup(apitypes.GenForeignKey)
	for typ, ok := range map[string]bool{"BIGINT": true, "INTEGER": true, "VARCHAR": true, "UUID": true, "DOUBLE": false, "DATE": false} {
		if err := g.Check(col("c", typ), nil); (err == nil) != ok {
			t.Errorf("Check on %s: err = %v", typ, err)
		}
	}
	base := Args{"table": "crm.customers", "column": "id"}
	n := g.Needs(col("c", "BIGINT"), base)
	if n.Parts != profile.PartFanOut || *n.Parent != (profile.Ref{Schema: "crm", Table: "customers", Column: "id"}) {
		t.Errorf("needs = %+v", n)
	}
	fixed := Args{"table": "crm.customers", "column": "id", "fanOut": map[string]any{"min": 1.0, "max": 2.0}}
	if n := g.Needs(col("c", "BIGINT"), fixed); n.Parts != 0 {
		t.Errorf("fixed fan-out: needs = %+v", n)
	}
	fixed["orphanRate"] = "source"
	if n := g.Needs(col("c", "BIGINT"), fixed); n.Parts != profile.PartFanOut {
		t.Errorf("source orphan rate: needs = %+v", n)
	}
}

func TestForeignKeyDraw(t *testing.T) {
	p := &profile.Column{FanOut: []profile.Bin{{Lo: 1, Hi: 1, Count: 50}, {Lo: 4, Hi: 4, Count: 50}}}
	const parents, rows = 40, 100
	cg, err := New(apitypes.GenForeignKey, fkCol("BIGINT", rows, intKeys(parents)...), p, Args{"table": "crm.customers", "column": "id"}, fkStreams("customer_id"))
	if err != nil {
		t.Fatal(err)
	}
	if cg.Pool() != parents {
		t.Errorf("pool = %d", cg.Pool())
	}
	vals := drawFK(t, cg, rows)
	per := map[any]int{}
	sorted := true
	for i, v := range vals {
		per[v]++
		if i > 0 && v.(int64) < vals[i-1].(int64) {
			sorted = false
		}
	}
	if sorted {
		t.Error("child rows are sorted by parent, want a shuffle")
	}
	st := cg.(FanOuter).FanOutStats()
	if st == nil || st.Parents != parents || st.Orphans != 0 {
		t.Fatalf("stats = %+v", st)
	}
	bound := int(math.Ceil(4 * st.Scale))
	for k, n := range per {
		if k.(int64) < 100 || k.(int64) >= 100+parents || n > bound {
			t.Errorf("key %v drawn %d times, bound %d", k, n, bound)
		}
	}
	// A redraw of the same row gives the same key.
	if v, _ := cg.Next(RowCtx{Index: 7}); v != vals[7] {
		t.Errorf("row 7: %v then %v", vals[7], v)
	}
	if _, err := cg.Next(RowCtx{Index: rows}); err == nil {
		t.Error("row past the slots: want an error")
	}

	// Uniform fan-out from args, no profile.
	cg, err = New(apitypes.GenForeignKey, fkCol("BIGINT", 30, intKeys(10)...), nil,
		Args{"table": "crm.customers", "column": "id", "fanOut": map[string]any{"min": 3.0, "max": 3.0}}, fkStreams("customer_id"))
	if err != nil {
		t.Fatal(err)
	}
	per = map[any]int{}
	for _, v := range drawFK(t, cg, 30) {
		per[v]++
	}
	for k, n := range per {
		if n != 3 {
			t.Errorf("fanOut 3: key %v drawn %d times", k, n)
		}
	}
}

func TestForeignKeyOrphans(t *testing.T) {
	args := Args{"table": "crm.customers", "column": "id", "fanOut": map[string]any{"min": 1.0, "max": 5.0}, "orphanRate": 0.2}
	cg, err := New(apitypes.GenForeignKey, fkCol("BIGINT", 2000, intKeys(500)...), nil, args, fkStreams("customer_id"))
	if err != nil {
		t.Fatal(err)
	}
	var orphans int64
	seen := map[any]bool{}
	for _, v := range drawFK(t, cg, 2000) {
		if k := v.(int64); k >= 600 {
			orphans++
			if seen[v] {
				t.Errorf("orphan key %v twice", v)
			}
			seen[v] = true
		}
	}
	if st := cg.(FanOuter).FanOutStats(); st.Orphans != orphans || math.Abs(float64(orphans)/2000-0.2) > 0.03 {
		t.Errorf("orphans %d, stats %+v", orphans, st)
	}

	keys := []any{"a", "b", "c", "d", "e"}
	cg, err = New(apitypes.GenForeignKey, fkCol("VARCHAR", 50, keys...), nil, args, fkStreams("customer_id"))
	if err != nil {
		t.Fatal(err)
	}
	orphans = 0
	for _, v := range drawFK(t, cg, 50) {
		if s := v.(string); len(s) == 36 && strings.Count(s, "-") == 4 {
			orphans++
		} else if !strings.Contains("abcde", s) {
			t.Errorf("value %q is no key and no UUID", s)
		}
	}
	if orphans == 0 || cg.(FanOuter).FanOutStats().Orphans != orphans {
		t.Errorf("text orphans %d, stats %+v", orphans, cg.(FanOuter).FanOutStats())
	}

	// orphanRate source reads the profile.
	src := Args{"table": "crm.customers", "column": "id", "orphanRate": "source"}
	p := &profile.Column{FanOut: []profile.Bin{{Lo: 2, Hi: 2, Count: 10}}, OrphanRate: 1}
	cg, err = New(apitypes.GenForeignKey, fkCol("BIGINT", 10, intKeys(5)...), p, src, fkStreams("customer_id"))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range drawFK(t, cg, 10) {
		if v.(int64) < 105 {
			t.Errorf("orphan rate 1 drew parent key %v", v)
		}
	}
}

func TestForeignKeyErrors(t *testing.T) {
	args := Args{"table": "crm.customers", "column": "id"}
	p := &profile.Column{FanOut: []profile.Bin{{Lo: 1, Hi: 1, Count: 5}}}
	for name, tc := range map[string]struct {
		col  Column
		p    *profile.Column
		args Args
	}{
		"no keys":    {fkCol("BIGINT", 10), p, args},
		"nil keys":   {col("customer_id", "BIGINT"), p, args},
		"no fan-out": {fkCol("BIGINT", 10, intKeys(3)...), &profile.Column{}, args},
		"double orphans": {func() Column {
			c := fkCol("HUGEINT", 10, 1.0, 2.0)
			c.Keys.Stage = StageDouble
			return c
		}(), p, Args{"table": "crm.customers", "column": "id", "orphanRate": 0.1}},
	} {
		if _, err := New(apitypes.GenForeignKey, tc.col, tc.p, tc.args, fkStreams("customer_id")); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// TestForeignKeyGolden pins the first keys for a fixed seed.
func TestForeignKeyGolden(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("golden values are pinned on amd64")
	}
	p := &profile.Column{FanOut: []profile.Bin{{Lo: 0, Hi: 0, Count: 5}, {Lo: 2, Hi: 2, Count: 10}, {Lo: 3, Hi: 3, Count: 5}}}
	args := Args{"table": "crm.customers", "column": "id", "orphanRate": 0.1}
	cg, err := New(apitypes.GenForeignKey, fkCol("BIGINT", 12, intKeys(6)...), p, args, fkStreams("customer_id"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fmt.Sprint(drawFK(t, cg, 12)), "[106 100 101 103 101 104 105 101 100 101 104 105]"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
