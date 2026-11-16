// SPDX-License-Identifier: AGPL-3.0-or-later

package executor_test

import (
	"context"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/executor"
)

// ExecTime leaves out the wait for a pool connection: a subject is not
// charged for time it spent queued behind other queries.
func TestExecutorExecTimeExcludesPoolWait(t *testing.T) {
	e := newExec(t, executor.Config{MaxOpen: 1, MaxIdle: 1})
	first, err := e.Execute(context.Background(), executor.Request{SQL: "SELECT 1"})
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	const hold = 300 * time.Millisecond
	go func() {
		time.Sleep(hold)
		_ = first.Rows.Close()
	}()

	began := time.Now()
	second, err := e.Execute(context.Background(), executor.Request{SQL: "SELECT 2"})
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	defer func() { _ = second.Rows.Close() }()
	if waited := time.Since(began); waited < hold {
		t.Fatalf("second Execute returned after %s, want it to wait for the only connection", waited)
	}
	if second.ExecTime <= 0 || second.ExecTime >= hold {
		t.Fatalf("ExecTime = %s, want > 0 and without the %s pool wait", second.ExecTime, hold)
	}
}
