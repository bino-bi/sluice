// SPDX-License-Identifier: AGPL-3.0-or-later

package executor_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
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

// The query timeout also covers delivery. A stream cut by it ends without
// a Scan error, so Err must report the deadline.
func TestExecutorTimeoutDuringDeliveryShowsInErr(t *testing.T) {
	e := newExec(t, executor.Config{})
	resp, err := e.Execute(context.Background(), executor.Request{
		SQL:     "SELECT * FROM range(100) t(x)",
		Timeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	defer func() { _ = resp.Rows.Close() }()
	if !resp.Rows.Next() {
		t.Fatalf("no first row: %v", resp.Rows.Err())
	}
	time.Sleep(400 * time.Millisecond)
	n := 0
	for resp.Rows.Next() {
		n++
	}
	if err := resp.Rows.Err(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Err = %v after %d more rows, want the deadline", err, n)
	}
}

// A result of exactly MaxRows rows is complete, not truncated.
func TestExecutorExactlyMaxRowsIsNotTruncated(t *testing.T) {
	e := newExec(t, executor.Config{})
	resp, err := e.Execute(context.Background(), executor.Request{SQL: "SELECT * FROM range(5) t(x)", MaxRows: 5})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	defer func() { _ = resp.Rows.Close() }()
	if n := drain(t, resp); n != 5 || resp.Truncated {
		t.Fatalf("rows %d, truncated %v; want 5, false", n, resp.Truncated)
	}
}

// A result cut by the row cap is fully delivered. A deadline that passes
// before Close is no error of those rows.
func TestExecutorCappedResultIgnoresLateDeadline(t *testing.T) {
	e := newExec(t, executor.Config{})
	resp, err := e.Execute(context.Background(), executor.Request{
		SQL: "SELECT * FROM range(100) t(x)", MaxRows: 3, Timeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	defer func() { _ = resp.Rows.Close() }()
	n := 0
	for resp.Rows.Next() {
		n++
	}
	time.Sleep(400 * time.Millisecond)
	if err := resp.Rows.Err(); err != nil || n != 3 || !resp.Truncated {
		t.Fatalf("rows %d, truncated %v, Err %v; want 3, true, nil", n, resp.Truncated, err)
	}
}

// panicValuer panics when database/sql converts it, inside QueryContext.
type panicValuer struct{}

func (panicValuer) Value() (driver.Value, error) { panic("driver bug") }

// A panic inside Execute frees the read lock, so a later Exclusive (a
// catalog swap) does not block for good, and Execute does not hang. A panic
// inside the driver loses its connection: the pool has a second one here.
func TestExecutorPanicFreesLock(t *testing.T) {
	e := newExec(t, executor.Config{MaxOpen: 2, MaxIdle: 2})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("Execute did not panic")
			}
		}()
		_, _ = e.Execute(ctx, executor.Request{SQL: "SELECT ?::INTEGER", Params: []any{panicValuer{}}})
	}()
	if err := e.Exclusive(ctx, func(context.Context, *sql.Conn) error { return nil }); err != nil {
		t.Fatalf("Exclusive after the panic: %v", err)
	}
	resp, err := e.Execute(ctx, executor.Request{SQL: "SELECT 2"})
	if err != nil {
		t.Fatalf("Execute after the panic: %v", err)
	}
	_ = resp.Rows.Close()
}
