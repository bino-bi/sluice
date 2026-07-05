// SPDX-License-Identifier: AGPL-3.0-or-later

package executor_test

import (
	"context"
	"testing"

	"github.com/bino-bi/sluice/internal/executor"
)

// TestExecutorUsingSampleSubquery pins the DuckDB syntax contract the
// rewriter's sample wrap relies on: `USING SAMPLE <pct>% (<method>)` must
// be accepted on a FROM-subquery for every whitelisted method. Rates are
// 100% so the row counts stay deterministic.
func TestExecutorUsingSampleSubquery(t *testing.T) {
	e := newExec(t, executor.Config{})

	for _, method := range []string{"reservoir", "bernoulli", "system"} {
		t.Run(method, func(t *testing.T) {
			resp, err := e.Execute(context.Background(), executor.Request{
				SQL: "SELECT * FROM (SELECT range AS id FROM range(100)) AS sluice_sample USING SAMPLE 100% (" + method + ")",
			})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			defer func() { _ = resp.Rows.Close() }()

			var count int
			for resp.Rows.Next() {
				var id int64
				if err := resp.Rows.Scan(&id); err != nil {
					t.Fatalf("scan: %v", err)
				}
				count++
			}
			if err := resp.Rows.Err(); err != nil {
				t.Fatalf("rows err: %v", err)
			}
			if count != 100 {
				t.Fatalf("count = %d; want 100 at rate 100%%", count)
			}
		})
	}
}
