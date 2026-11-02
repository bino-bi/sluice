// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import "testing"

func TestDisplayType(t *testing.T) {
	for in, want := range map[string]string{
		"ENUM('M', 'W', 'D', 'X')":            "ENUM",
		"ENUM('a)b', 'it''s')[]":              "ENUM[]",
		"STRUCT(g ENUM('x', 'y'), n INTEGER)": "STRUCT(g ENUM, n INTEGER)",
		"MAP(VARCHAR, ENUM('p'))":             "MAP(VARCHAR, ENUM)",
		"DECIMAL(12,2)":                       "DECIMAL(12,2)",
		"TIMESTAMP WITH TIME ZONE":            "TIMESTAMP WITH TIME ZONE",
	} {
		if got := DisplayType(in); got != want {
			t.Errorf("DisplayType(%q) = %q, want %q", in, got, want)
		}
	}
}
