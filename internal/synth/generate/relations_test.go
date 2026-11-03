// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

import (
	"fmt"
	"strings"
	"testing"
	"time"

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
