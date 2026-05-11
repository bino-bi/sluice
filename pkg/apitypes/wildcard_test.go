// SPDX-License-Identifier: Apache-2.0

package apitypes

import "testing"

func TestWildcardMatcher(t *testing.T) {
	t.Parallel()
	cases := []struct {
		pattern string
		path    string
		match   bool
	}{
		{"pg.*.orders", "pg.public.orders", true},
		{"pg.*.orders", "pg.public.orders.archive", false},
		{"pg.*.orders", "pg.public.customers", false},
		{"pg.**", "pg.public.orders", true},
		{"pg.**", "pg", false},
		{"pg.**", "mysql.public.orders", false},
		{"analytics_*", "analytics_events", true},
		{"analytics_*", "analytics_", true},
		{"analytics_*", "analytics.events", false},
		{"**.orders", "pg.public.orders", true},
		{"**.orders", "orders", false},
		{`\*.orders`, "*.orders", true},
		{`\*.orders`, "public.orders", false},
		{"exact", "exact", true},
		{"exact", "other", false},
	}
	for _, c := range cases {
		t.Run(c.pattern+"=>"+c.path, func(t *testing.T) {
			t.Parallel()
			m, err := CompileWildcard(c.pattern)
			if err != nil {
				t.Fatalf("CompileWildcard(%q): %v", c.pattern, err)
			}
			if got := m.Match(c.path); got != c.match {
				t.Errorf("Match(%q) = %v, want %v", c.path, got, c.match)
			}
			if m.Pattern() != c.pattern {
				t.Errorf("Pattern() = %q, want %q", m.Pattern(), c.pattern)
			}
		})
	}
}

func TestCompileWildcardErrors(t *testing.T) {
	t.Parallel()
	cases := []string{"", `pattern\`}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			t.Parallel()
			if _, err := CompileWildcard(c); err == nil {
				t.Errorf("CompileWildcard(%q) should fail", c)
			}
		})
	}
}
