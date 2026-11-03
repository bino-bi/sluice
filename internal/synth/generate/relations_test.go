// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

func names(ns ...string) []Column {
	out := make([]Column, len(ns))
	for i, n := range ns {
		out[i] = Column{Name: n}
	}
	return out
}

func TestOrder(t *testing.T) {
	cases := []struct {
		name string
		cols []Column
		refs [][]string
		want string
	}{
		{"no refs", names("a", "b", "c"), nil, "[0 1 2]"},
		{"ref later column", names("Email", "first", "last"), [][]string{{"first", "last"}}, "[1 2 0]"},
		// Once c is placed, a and d are ready; the lower ordinal wins.
		{"tie-break", names("a", "b", "c", "d"), [][]string{{"c"}, {"a"}, nil, nil}, "[2 0 1 3]"},
		{"chain", names("a", "b", "c"), [][]string{{"b"}, {"c"}, nil}, "[2 1 0]"},
	}
	for _, tc := range cases {
		got, err := Order(tc.cols, tc.refs)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if fmt.Sprint(got) != tc.want {
			t.Errorf("%s: order = %v, want %s", tc.name, got, tc.want)
		}
	}
	if _, err := Order(names("a", "b", "c"), [][]string{{"b"}, {"a"}, nil}); err == nil || err.Error() != "columns a, b depend on each other" {
		t.Errorf("cycle: err = %v", err)
	}
	if _, err := Order(names("a"), [][]string{{"nope"}}); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("unknown ref: err = %v", err)
	}
}

// templateCol is a VARCHAR column whose siblings cover every stage.
func templateCol() Column {
	c := col("out", "VARCHAR")
	c.Siblings = map[string]Sibling{}
	for i, s := range []struct{ name, typ string }{
		{"S", "VARCHAR"}, {"i", "BIGINT"}, {"f", "DOUBLE"}, {"b", "BOOLEAN"},
		{"d", "DATE"}, {"ts", "TIMESTAMP"}, {"tm", "TIME"}, {"out", "VARCHAR"},
	} {
		c.Siblings[strings.ToLower(s.name)] = Sibling{Index: i, Name: s.name, Type: s.typ}
	}
	return c
}

func TestTemplate(t *testing.T) {
	c := templateCol()
	ts := time.Date(2024, 3, 5, 7, 8, 9, 120000000, time.UTC)
	row := RowCtx{Values: []any{"Ann", int64(-42), 2.5, true,
		time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC), ts, float64(7*3600 + 5*60 + 3), nil}}
	cases := []struct {
		tmpl  string
		lower bool
		want  any
	}{
		{"{s}|{I}|{f}|{b}|{d}|{ts}|{tm}", false, "Ann|-42|2.5|true|2024-01-05|2024-03-05 07:08:09.12|07:05:03"},
		{"{{{s}}} {{x}}", true, "{ann} {x}"},
		{"{s}@{i}.ORG", true, "ann@-42.org"},
	}
	g, _ := Lookup(apitypes.GenTemplate)
	for _, tc := range cases {
		args := Args{"template": tc.tmpl, "lower": tc.lower}
		if err := g.Check(c, args); err != nil {
			t.Fatalf("%s: Check: %v", tc.tmpl, err)
		}
		cg, err := New(apitypes.GenTemplate, c, nil, args, streams("out"))
		if err != nil {
			t.Fatalf("%s: New: %v", tc.tmpl, err)
		}
		if got, err := cg.Next(row); err != nil || got != tc.want {
			t.Errorf("%s: got %v, %v, want %v", tc.tmpl, got, err, tc.want)
		}
		if cg.Pool() != -1 {
			t.Errorf("pool = %d", cg.Pool())
		}
	}

	// A NULL ref gives NULL. Without nullRate in args and no profile, no
	// other row is NULL.
	cg, err := New(apitypes.GenTemplate, c, nil, Args{"template": "{s}-{d}"}, streams("out"))
	if err != nil {
		t.Fatal(err)
	}
	r := RowCtx{Values: append([]any(nil), row.Values...)}
	for i := range 200 {
		r.Values[0] = "x"
		if i%3 == 0 {
			r.Values[0] = nil
		}
		v, err := cg.Next(r)
		if err != nil {
			t.Fatal(err)
		}
		if (v == nil) != (i%3 == 0) {
			t.Fatalf("row %d: value %v with ref %v", i, v, r.Values[0])
		}
	}

	for _, bad := range []struct{ args Args }{
		{Args{"template": "{nope}"}},
		{Args{"template": "{s"}},
	} {
		if err := g.Check(c, bad.args); err == nil {
			t.Errorf("%v: want Check error", bad.args)
		}
	}
	if err := g.Check(col("n", "BIGINT"), Args{"template": "{s}"}); err == nil {
		t.Error("template on BIGINT: want type error")
	}
}

// A ref shows the value the snapshot column holds after the cast of its
// float64 stage. The wants are DuckDB casts of the same doubles.
func TestTemplateFormatCast(t *testing.T) {
	for _, tc := range []struct {
		v    float64
		typ  string
		want string
	}{
		{9993.977092152212, "DECIMAL(12,2)", "9993.98"},
		{0.125, "DECIMAL(12,2)", "0.13"},
		{-0.125, "DECIMAL(12,2)", "-0.13"},
		{1.005, "DECIMAL(12,2)", "1.00"},
		{10.5, "DECIMAL(12,2)", "10.50"},
		{-0.001, "DECIMAL(12,2)", "0.00"},
		{2.5, "DECIMAL(12,0)", "3"},
		{0.05, "DECIMAL(4,3)", "0.050"},
		{2.5, "HUGEINT", "2"},
		{3.5, "UBIGINT", "4"},
		{1e20, "HUGEINT", "100000000000000000000"},
		{123456789.123, "FLOAT", "123456790"},
		{0.1, "FLOAT", "0.1"},
		{56111.5884385, "TIME", "15:35:11.588438"},
		{56111.5, "TIME", "15:35:11.5"},
		{1.5e-6, "TIME", "00:00:00.000002"},
		{2.5e-6, "TIME", "00:00:00.000002"},
		{86399.9999999, "TIME", "00:00:00"},
		{0.1, "DOUBLE", "0.1"},
	} {
		if got := formatValue(tc.v, tc.typ); got != tc.want {
			t.Errorf("%v as %s = %s, want %s", tc.v, tc.typ, got, tc.want)
		}
	}
}

// offsetCol is a column of type typ whose from column ordered has type
// fromType at index 0.
func offsetCol(typ, fromType string) Column {
	c := col("shipped", typ)
	c.Siblings = map[string]Sibling{
		"ordered": {Index: 0, Name: "Ordered", Type: fromType},
		"shipped": {Index: 1, Name: "shipped", Type: typ},
	}
	return c
}

func TestDateOffsetCheck(t *testing.T) {
	g, _ := Lookup(apitypes.GenDateOffset)
	from := Args{"from": "ORDERED"}
	for _, tc := range []struct {
		typ, fromType string
		args          Args
		want          string
	}{
		{"DATE", "DATE", from, ""},
		{"TIMESTAMP", "TIMESTAMP WITH TIME ZONE", from, ""},
		{"DATE", "TIMESTAMP", from, "dateOffset needs a DATE column in from, Ordered has type TIMESTAMP"},
		{"TIMESTAMP", "DATE", from, "dateOffset needs a TIMESTAMP column in from, Ordered has type DATE"},
		{"DATE", "ENUM('a', 'b')", from, "dateOffset needs a DATE column in from, Ordered has type ENUM"},
		{"DATE", "DATE", Args{"from": "nope"}, "dateOffset names column nope, which is not in the table"},
		{"VARCHAR", "DATE", from, "generator does not fit the column type: dateOffset on VARCHAR"},
		{"DATE", "DATE", Args{"from": "ordered", "offset": map[string]any{"min": 0.5, "max": 1.0}}, ""},
		{"DATE", "DATE", Args{"from": "ordered", "offset": map[string]any{"min": 0.4, "max": 0.6}}, "dateOffset on a DATE needs a whole day in the offset span, {min: 0.4, max: 0.6} has none"},
		{"DATE", "DATE", Args{"from": "ordered", "offset": map[string]any{"min": -0.8, "max": -0.2}}, "dateOffset on a DATE needs a whole day in the offset span, {min: -0.8, max: -0.2} has none"},
		{"TIMESTAMP", "TIMESTAMP", Args{"from": "ordered", "offset": map[string]any{"min": 0.4, "max": 0.6}}, ""},
	} {
		err := g.Check(offsetCol(tc.typ, tc.fromType), tc.args)
		if got := fmt.Sprint(err); (tc.want == "" && err != nil) || (tc.want != "" && got != tc.want) {
			t.Errorf("%s from %s: err = %v, want %q", tc.typ, tc.fromType, err, tc.want)
		}
	}
}

func TestDateOffsetNeeds(t *testing.T) {
	g, _ := Lookup(apitypes.GenDateOffset)
	c := offsetCol("DATE", "DATE")
	n := g.Needs(c, Args{"from": "ordered"})
	if n.Parts != profile.PartOffset|profile.PartQuantiles || n.From != "Ordered" || !n.KeepSign || n.Unit != 1 {
		t.Errorf("source needs = %+v", n)
	}
	n = g.Needs(c, Args{"from": "ordered", "offset": map[string]any{"min": 1.0, "max": 2.0}})
	if n.Parts != profile.PartOffset {
		t.Errorf("span needs = %+v", n)
	}
}

func TestDateOffsetDraw(t *testing.T) {
	day := time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC)
	ts := time.Date(2024, 1, 31, 23, 59, 59, 500000, time.UTC)
	for _, tc := range []struct {
		typ    string
		from   time.Time
		args   Args
		p      *profile.Column
		lo, hi time.Duration
		step   time.Duration
	}{
		{"DATE", day, Args{"offset": map[string]any{"min": -2.0, "max": 3.0}}, nil, -2 * 24 * time.Hour, 3 * 24 * time.Hour, 24 * time.Hour},
		{"TIMESTAMP", ts, Args{"offset": map[string]any{"min": 0.0, "max": 0.5}}, nil, 0, 12 * time.Hour, time.Second},
		// Rounding to whole days stays inside the span.
		{"DATE", day, Args{"offset": map[string]any{"min": 0.4, "max": 3.6}}, nil, 24 * time.Hour, 3 * 24 * time.Hour, 24 * time.Hour},
		// Quantiles are in days for DATE and in seconds for TIMESTAMP.
		{"DATE", day, nil, &profile.Column{Quantiles: []float64{1, 2, 10}}, 24 * time.Hour, 10 * 24 * time.Hour, 24 * time.Hour},
		{"TIMESTAMP", ts, nil, &profile.Column{Quantiles: []float64{60, 3600}}, time.Minute, time.Hour, time.Second},
	} {
		args := Args{"from": "ordered"}
		for k, v := range tc.args {
			args[k] = v
		}
		cg, err := New(apitypes.GenDateOffset, offsetCol(tc.typ, tc.typ), tc.p, args, streams("shipped"))
		if err != nil {
			t.Fatalf("%s %v: %v", tc.typ, tc.args, err)
		}
		seen := map[time.Duration]bool{}
		for i := range 500 {
			row := RowCtx{Index: int64(i), Values: []any{tc.from, nil}}
			if i%5 == 0 {
				row.Values[0] = nil
			}
			v, err := cg.Next(row)
			if err != nil {
				t.Fatal(err)
			}
			if row.Values[0] == nil {
				if v != nil {
					t.Fatalf("%s: NULL from gave %v", tc.typ, v)
				}
				continue
			}
			d := v.(time.Time).Sub(tc.from)
			if d < tc.lo || d > tc.hi || d%tc.step != 0 {
				t.Fatalf("%s %v: offset %v outside [%v, %v] or not a whole %v", tc.typ, tc.args, d, tc.lo, tc.hi, tc.step)
			}
			seen[d] = true
		}
		if len(seen) < 3 {
			t.Errorf("%s %v: only %d distinct offsets", tc.typ, tc.args, len(seen))
		}
	}
	if _, err := New(apitypes.GenDateOffset, offsetCol("DATE", "DATE"), &profile.Column{}, Args{"from": "ordered"}, streams("shipped")); err == nil {
		t.Error("source offset without a grid: want an error")
	}
}

// TestDateOffsetFarOffset checks an offset above the 292 years a
// time.Duration holds, as a source offset to 9999-12-31 gives.
func TestDateOffsetFarOffset(t *testing.T) {
	from := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	want := time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
	o := float64(want.Unix() - from.Unix())
	cg, err := New(apitypes.GenDateOffset, offsetCol("TIMESTAMP", "TIMESTAMP"), &profile.Column{Quantiles: []float64{o, o}}, Args{"from": "ordered"}, streams("shipped"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := cg.Next(RowCtx{Values: []any{from, nil}})
	if err != nil {
		t.Fatal(err)
	}
	if !v.(time.Time).Equal(want) {
		t.Errorf("got %v, want %v", v, want)
	}
}

// TestDateOffsetGolden pins the first values for a fixed seed.
func TestDateOffsetGolden(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("golden values are pinned on amd64")
	}
	from := time.Date(2024, 2, 27, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		typ  string
		args Args
		p    *profile.Column
		want string
	}{
		{"DATE", nil, &profile.Column{Quantiles: []float64{0, 1, 3, 9}},
			"2024-03-04T00:00:00Z 2024-03-01T00:00:00Z 2024-03-02T00:00:00Z 2024-02-27T00:00:00Z 2024-03-03T00:00:00Z 2024-02-28T00:00:00Z"},
		{"TIMESTAMP", Args{"offset": map[string]any{"min": -1.0, "max": 1.0}}, nil,
			"2024-02-28T01:49:23Z 2024-02-27T15:49:03Z 2024-02-27T21:52:45Z 2024-02-26T15:57:15Z 2024-02-27T23:05:44Z 2024-02-26T23:06:49Z"},
	} {
		args := Args{"from": "ordered"}
		for k, v := range tc.args {
			args[k] = v
		}
		f := from
		if tc.typ == "DATE" {
			f = time.Date(2024, 2, 27, 0, 0, 0, 0, time.UTC)
		}
		cg, err := New(apitypes.GenDateOffset, offsetCol(tc.typ, tc.typ), tc.p, args, streams("shipped"))
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for i := range 6 {
			v, err := cg.Next(RowCtx{Index: int64(i), Values: []any{f, nil}})
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, v.(time.Time).Format(time.RFC3339))
		}
		if got := strings.Join(out, " "); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.typ, got, tc.want)
		}
	}
}
