// SPDX-License-Identifier: AGPL-3.0-or-later

package parser

import (
	"reflect"
	"testing"
)

func TestExtractTablesRegex(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want []TableRef
	}{
		{
			name: "simple FROM",
			sql:  "SELECT 1 FROM orders",
			want: []TableRef{{Table: "orders"}},
		},
		{
			name: "schema-qualified",
			sql:  "SELECT * FROM public.orders",
			want: []TableRef{{Schema: "public", Table: "orders"}},
		},
		{
			name: "catalog-qualified",
			sql:  "SELECT * FROM pg.public.orders",
			want: []TableRef{{Catalog: "pg", Schema: "public", Table: "orders"}},
		},
		{
			name: "JOIN",
			sql:  "SELECT * FROM a JOIN b ON a.x=b.x",
			want: []TableRef{{Table: "a"}, {Table: "b"}},
		},
		{
			name: "multiple tables deduped",
			sql:  "SELECT * FROM orders UNION SELECT * FROM orders",
			want: []TableRef{{Table: "orders"}},
		},
		{
			name: "quoted identifiers",
			sql:  `SELECT * FROM "My Schema"."My Table"`,
			want: []TableRef{{Schema: "My Schema", Table: "My Table"}},
		},
		{
			name: "FROM inside comment is ignored",
			sql:  "SELECT 1 -- FROM stolen\nFROM orders",
			want: []TableRef{{Table: "orders"}},
		},
		{
			name: "FROM inside string is ignored",
			sql:  "SELECT 'from secrets' FROM orders",
			want: []TableRef{{Table: "orders"}},
		},
		{
			name: "FROM inside block comment is ignored",
			sql:  "SELECT /* FROM sneaky */ 1 FROM orders",
			want: []TableRef{{Table: "orders"}},
		},
		{
			name: "empty input",
			sql:  "",
			want: nil,
		},
		{
			name: "no tables",
			sql:  "SELECT 1+1",
			want: nil,
		},
		{
			name: "case insensitive keyword",
			sql:  "select * from orders",
			want: []TableRef{{Table: "orders"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractTablesRegex(tc.sql)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ExtractTablesRegex(%q)\n  got:  %#v\n  want: %#v", tc.sql, got, tc.want)
			}
		})
	}
}

func TestStripCommentsAndStringsPreservesOffsets(t *testing.T) {
	sql := "SELECT 'hello' -- comment\nFROM t"
	stripped := stripCommentsAndStrings(sql)
	if len(stripped) != len(sql) {
		t.Fatalf("stripped length %d != input length %d", len(stripped), len(sql))
	}
}

func TestUnquoteIdent(t *testing.T) {
	cases := map[string]string{
		``:             ``,
		`foo`:          `foo`,
		`"foo"`:        `foo`,
		`"foo""bar"`:   `foo"bar`,
		`"with space"`: `with space`,
	}
	for in, want := range cases {
		if got := unquoteIdent(in); got != want {
			t.Errorf("unquoteIdent(%q) = %q; want %q", in, got, want)
		}
	}
}
