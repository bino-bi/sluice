// SPDX-License-Identifier: AGPL-3.0-or-later

package parser

import "testing"

func TestQuoteIdent(t *testing.T) {
	cases := map[string]string{
		"customers":  `"customers"`,
		`a"b`:        `"a""b"`,
		"first name": `"first name"`,
	}
	for in, want := range cases {
		if got := QuoteIdent(in); got != want {
			t.Errorf("QuoteIdent(%q) = %s, want %s", in, got, want)
		}
	}
}
