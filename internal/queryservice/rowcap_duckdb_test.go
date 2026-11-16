// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/queryservice"
)

// rowCapSeed adds a table of 50 rows without a row filter or mask.
const rowCapSeed = `
CREATE TABLE logs (id INTEGER);
INSERT INTO logs SELECT i FROM range(1, 51) t(i);
`

const rowCapPolicy = `
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: allow-logs
  priority: 100
spec:
  effect: allow
  match:
    any:
      - subjects:
          groups: ["analytics"]
        resources: { catalogs: ["shop"], schemas: ["main"], tables: ["logs"] }
`

// The row limit reaches DuckDB as an outer LIMIT of maxRows+1, so DuckDB
// does not build rows the client never gets. A smaller LIMIT stays, and
// the result is the same as without the cap.
func TestExecute_RowCapReachesDuckDB(t *testing.T) {
	svc, ex, _ := newSeededHelloSluiceServiceOpts(t, nil, rowCapSeed, func(o *queryservice.Options) {
		o.Limits.DefaultMaxRows = 10
	}, rowCapPolicy)

	cases := []struct {
		name      string
		sql       string
		rows      int
		truncated bool
		// wantSQL is a part of the SQL DuckDB ran. "=" means the client's
		// text as it is.
		wantSQL string
		first   int32
		maxRows int64
	}{
		{"plain select", "SELECT id FROM shop.main.logs", 10, true, "\nLIMIT 11", 0, 0},
		{"order by", "SELECT id FROM shop.main.logs ORDER BY id DESC", 10, true, "\nLIMIT 11", 50, 0},
		{"trailing comment", "SELECT id FROM shop.main.logs ORDER BY id -- all of them", 10, true, "\nLIMIT 11", 1, 0},
		{"trailing semicolon", "SELECT id FROM shop.main.logs ORDER BY id;", 10, true, "\nLIMIT 11;", 1, 0},
		{"set operation", "SELECT id FROM shop.main.logs UNION ALL SELECT id FROM shop.main.logs", 10, true, "\nLIMIT 11", 0, 0},
		{"smaller limit stays", "SELECT id FROM shop.main.logs ORDER BY id LIMIT 5", 5, false, "=", 1, 0},
		{"larger limit is lowered", "SELECT id FROM shop.main.logs ORDER BY id LIMIT 1000", 10, true, "LIMIT 11", 1, 0},
		{"offset keeps the text", "SELECT id FROM shop.main.logs ORDER BY id OFFSET 45", 5, false, "=", 46, 0},
		{"filtered table", "SELECT id FROM shop.main.orders ORDER BY id", 3, false, "LIMIT 11", 101, 0},
		{"filtered table, request row limit", "SELECT id FROM shop.main.orders ORDER BY id", 2, true, "LIMIT 3", 101, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
				SQL: tc.sql, User: helloUser(), Origin: queryservice.OriginREST, MaxRows: tc.maxRows,
			})
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			rows := drainRows(t, res)
			if len(rows) != tc.rows || res.Truncated != tc.truncated {
				t.Fatalf("got %d rows, truncated %v; want %d, %v", len(rows), res.Truncated, tc.rows, tc.truncated)
			}
			if tc.first != 0 && rows[0][0] != tc.first {
				t.Fatalf("first row %v, want %d", rows[0][0], tc.first)
			}
			if tc.wantSQL == "=" {
				if ex.last.SQL != tc.sql {
					t.Fatalf("DuckDB ran %q, want the client's text", ex.last.SQL)
				}
			} else if !strings.Contains(ex.last.SQL, tc.wantSQL) {
				t.Fatalf("DuckDB ran %q, want it to hold %q", ex.last.SQL, tc.wantSQL)
			}
		})
	}
}
