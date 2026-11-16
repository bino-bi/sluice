// SPDX-License-Identifier: Apache-2.0

package mask_test

import (
	"database/sql"
	"strings"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/bino-bi/sluice/pkg/mask"
)

func openDuckDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestPartialSQL_ShortValues runs the partial snippet in DuckDB. A value
// not longer than showFirst+showLast must not show any character.
func TestPartialSQL_ShortValues(t *testing.T) {
	db := openDuckDB(t)
	p, _ := mask.Default().Lookup("partial")
	cases := []struct {
		in          string
		first, last int
		want        string
	}{
		{"abcdef", 4, 4, "******"},
		{"abcdefgh", 4, 4, "********"},
		{"abcdefghi", 4, 4, "abcd*fghi"},
		{"ab", 0, 2, "**"},
		{"abc", 0, 2, "*bc"},
		{"abc", 3, 0, "***"},
		{"abcd", 3, 0, "abc*"},
		{"", 1, 1, ""},
		{"1234567890123456", 0, 4, "************3456"},
	}
	for _, tc := range cases {
		snippet, params, err := p.MaskSQL(mask.MaskContext{Args: mask.Args{ShowFirst: tc.first, ShowLast: tc.last}})
		if err != nil {
			t.Fatalf("MaskSQL: %v", err)
		}
		q := "SELECT " + strings.ReplaceAll(snippet, "__col__", "v") +
			" FROM (SELECT '" + tc.in + "' AS v)"
		args := make([]any, len(params))
		for i, prm := range params {
			args[i] = prm.Value
		}
		var got string
		if err := db.QueryRow(q, args...).Scan(&got); err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("partial(%q, %d, %d) = %q, want %q", tc.in, tc.first, tc.last, got, tc.want)
		}
	}
}
