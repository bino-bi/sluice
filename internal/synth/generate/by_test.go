// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/internal/synth/rng"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// byCol is a VARCHAR column "out" with by siblings of every matchable
// stage at the indexes 0 to 3.
func byCol() Column {
	c := col("out", "VARCHAR")
	c.Siblings = map[string]Sibling{}
	for i, s := range []struct{ name, typ string }{
		{"Status", "VARCHAR"}, {"n", "INTEGER"}, {"flag", "BOOLEAN"}, {"day", "DATE"}, {"out", "VARCHAR"},
		{"big", "UBIGINT"}, {"amount", "DOUBLE"}, {"kind", "ENUM('a', 'b')"}, {"id", "UUID"},
	} {
		c.Siblings[strings.ToLower(s.name)] = Sibling{Index: i, Name: s.name, Type: s.typ}
	}
	return c
}

func nominal(vals ...string) *profile.Column {
	p := &profile.Column{}
	for _, v := range vals {
		p.TopValues.Values = append(p.TopValues.Values, profile.Value{V: v, Count: 10})
	}
	return p
}

func groupKey(vals ...any) string {
	ns := make([]sql.NullString, len(vals))
	for i, v := range vals {
		if s, ok := v.(string); ok {
			ns[i] = sql.NullString{String: s, Valid: true}
		}
	}
	return profile.GroupKey(ns)
}

func TestByCheck(t *testing.T) {
	c := byCol()
	for _, tc := range []struct {
		gen  apitypes.SyntheticGenerator
		typ  string
		by   []any
		want string
	}{
		{apitypes.GenCategorical, "VARCHAR", []any{"status", "N", "flag"}, ""},
		{apitypes.GenCategorical, "VARCHAR", []any{"day", "kind"}, ""},
		{apitypes.GenDistribution, "DOUBLE", []any{"status"}, ""},
		{apitypes.GenDateDistribution, "DATE", []any{"status"}, ""},
		{apitypes.GenCategorical, "VARCHAR", []any{"nope"}, "by names column nope, which is not in the table"},
		{apitypes.GenCategorical, "VARCHAR", []any{"big"}, "by column big has type UBIGINT, which cannot be matched"},
		{apitypes.GenDistribution, "DOUBLE", []any{"status", "amount"}, "by column amount has type DOUBLE, which cannot be matched"},
		{apitypes.GenDateDistribution, "DATE", []any{"id"}, "by column id has type UUID, which cannot be matched"},
	} {
		g, _ := Lookup(tc.gen)
		cc := c
		cc.Type = tc.typ
		err := g.Check(cc, Args{"by": tc.by})
		if got := fmt.Sprint(err); (tc.want == "" && err != nil) || (tc.want != "" && got != tc.want) {
			t.Errorf("%s by %v: err = %v, want %q", tc.gen, tc.by, err, tc.want)
		}
	}
}

func TestByGen(t *testing.T) {
	groups := []profile.Group{
		{Key: groupKey("x", "7", "true", "2024-01-05"), Profile: nominal("c")},
		{Key: groupKey(nil, "7", "false", "2024-01-05"), Profile: nominal("e")},
	}
	args := Args{"by": []any{"status", "n", "flag", "day"}}
	cg, err := NewBy(apitypes.GenCategorical, byCol(), nominal("a"), groups, args, streams("out"))
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		vals []any
		want string
	}{
		{[]any{"x", int64(7), true, day}, "c"},
		{[]any{nil, int64(7), false, day}, "e"},
		{[]any{"x", int64(8), true, day}, "a"},
		{[]any{"y", int64(7), true, day}, "a"},
		{[]any{"x", int64(7), true, nil}, "a"},
	} {
		v, err := cg.Next(RowCtx{Values: append(tc.vals, nil)})
		if err != nil {
			t.Fatal(err)
		}
		if v != tc.want {
			t.Errorf("by %v: got %v, want %s", tc.vals, v, tc.want)
		}
	}
	if fb, total := cg.(ByCounter).ByRows(); fb != 3 || total != 5 {
		t.Errorf("fallback %d of %d, want 3 of 5", fb, total)
	}
}

// unique holds across groups: one seen map for the column.
func TestByUnique(t *testing.T) {
	groups := []profile.Group{
		{Key: groupKey("x"), Profile: nominal("a")},
		{Key: groupKey("y"), Profile: nominal("a", "b")},
	}
	c := byCol()
	c.Rows = 3
	cg, err := NewBy(apitypes.GenCategorical, c, nominal("a", "b", "c"), groups,
		Args{"by": []any{"status"}, "unique": true}, streams("out"))
	if err != nil {
		t.Fatal(err)
	}
	row := func(s string) RowCtx { return RowCtx{Values: []any{s, nil, nil, nil, nil}} }
	if v, err := cg.Next(row("x")); v != "a" || err != nil {
		t.Fatalf("x: %v %v", v, err)
	}
	if v, err := cg.Next(row("y")); v != "b" || err != nil {
		t.Fatalf("y: %v %v, want b", v, err)
	}
	if _, err := cg.Next(row("x")); err == nil {
		t.Fatal("x again: want an error, a was drawn before")
	}
}

// unique fails at the start when the global and group pools together
// hold fewer values than rows, as it does without by.
func TestByUniquePool(t *testing.T) {
	groups := []profile.Group{{Key: groupKey("x"), Profile: nominal("a")}}
	args := Args{"by": []any{"status"}, "unique": true}
	_, err := NewBy(apitypes.GenCategorical, byCol(), nominal("a", "b", "c"), groups, args, streams("out"))
	if err == nil || err.Error() != "column out: unique needs 1000 values, the generator has only 4" {
		t.Errorf("err = %v", err)
	}
	c := byCol()
	c.Type = "DOUBLE"
	if _, err := NewBy(apitypes.GenDistribution, c, numberProfile(), nil, args, streams("out")); err != nil {
		t.Errorf("unknown pool: %v", err)
	}
}

// The null rate is the global one, drawn once per row from the nulls
// stream, whatever the group.
func TestByNullRate(t *testing.T) {
	global := numberProfile()
	global.NullRate = 0.25
	group := numberProfile()
	group.NullRate = 1
	groups := []profile.Group{{Key: groupKey("x"), Profile: group}}
	c := byCol()
	c.Type = "DOUBLE"
	cg, err := NewBy(apitypes.GenDistribution, c, global, groups, Args{"by": []any{"status"}}, streams("out"))
	if err != nil {
		t.Fatal(err)
	}
	ref := rng.For(testSeed, "syn", "crm.t", "out", rng.PurposeNulls)
	var nulls int
	for i := range 2000 {
		s := "x"
		if i%3 == 0 {
			s = "z"
		}
		v, err := cg.Next(RowCtx{Index: int64(i), Values: []any{s, nil, nil, nil, nil}})
		if err != nil {
			t.Fatal(err)
		}
		if (ref.Float64() < 0.25) != (v == nil) {
			t.Fatalf("row %d: NULL pattern differs from one draw per row", i)
		}
		if v == nil {
			nulls++
		}
	}
	if nulls < 400 || nulls > 600 {
		t.Errorf("%d NULLs of 2000, want about 500", nulls)
	}
}

func TestByErrors(t *testing.T) {
	args := Args{"by": []any{"status"}}
	if _, err := NewBy(apitypes.GenCategorical, byCol(), nominal("a"), nil, Args{"by": []any{"gone"}}, streams("out")); err == nil ||
		err.Error() != "column out: by names column gone, which is not in the table" {
		t.Errorf("unknown by column: %v", err)
	}
	bad := []profile.Group{{Key: groupKey("x"), Profile: &profile.Column{}}}
	if _, err := NewBy(apitypes.GenCategorical, byCol(), nominal("a"), bad, args, streams("out")); err == nil {
		t.Error("empty group profile: want an error")
	}
	if _, err := NewBy(apitypes.GenCategorical, byCol(), &profile.Column{}, nil, args, streams("out")); err == nil {
		t.Error("empty global profile: want an error")
	}
}
