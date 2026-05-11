// SPDX-License-Identifier: Apache-2.0

package datasource

import "testing"

func TestTableKeyRoundTrip(t *testing.T) {
	t.Parallel()
	key := TableKey{Catalog: "pg", Schema: "public", Table: "orders"}
	got := key.String()
	want := "pg.public.orders"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	parsed, err := ParseTableKey(got)
	if err != nil {
		t.Fatalf("ParseTableKey: %v", err)
	}
	if parsed != key {
		t.Errorf("round-trip mismatch: in=%+v out=%+v", key, parsed)
	}
}

func TestParseTableKeyRejects(t *testing.T) {
	t.Parallel()
	cases := []string{"", "a", "a.b", "a.b.c.d", ".b.c", "a..c", "a.b."}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseTableKey(c); err == nil {
				t.Errorf("ParseTableKey(%q) should fail", c)
			}
		})
	}
}
