// SPDX-License-Identifier: AGPL-3.0-or-later

package executor_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/executor"
)

func runQuery(ctx context.Context, e *executor.Executor, q string) (int, error) {
	resp, err := e.Execute(ctx, executor.Request{SQL: q})
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Rows.Close() }()
	n := 0
	for resp.Rows.Next() {
		n++
	}
	return n, resp.Rows.Err()
}

func TestExclusiveBlocksNewExecute(t *testing.T) {
	e := newExec(t, executor.Config{MaxOpen: 4})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan error, 1)
	err := e.Exclusive(ctx, func(ctx context.Context, _ *sql.Conn) error {
		go func() {
			_, err := runQuery(ctx, e, "SELECT 1")
			done <- err
		}()
		select {
		case <-done:
			t.Error("Execute finished while Exclusive ran")
		case <-time.After(200 * time.Millisecond):
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Exclusive: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Execute: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Execute did not finish after Exclusive returned")
	}
}

func TestExclusiveDoesNotWaitForOpenResult(t *testing.T) {
	e := newExec(t, executor.Config{MaxOpen: 4})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const rows = 100000
	resp, err := e.Execute(ctx, executor.Request{SQL: "SELECT * FROM range(100000)"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	defer func() { _ = resp.Rows.Close() }()
	if !resp.Rows.Next() {
		t.Fatalf("no first row: %v", resp.Rows.Err())
	}

	ran := false
	if err := e.Exclusive(ctx, func(context.Context, *sql.Conn) error {
		ran = true
		return nil
	}); err != nil {
		t.Fatalf("Exclusive: %v", err)
	}
	if !ran {
		t.Fatal("fn did not run")
	}
	n := 1
	for resp.Rows.Next() {
		n++
	}
	if err := resp.Rows.Err(); err != nil || n != rows {
		t.Errorf("open result read %d rows, %v; want %d", n, err, rows)
	}
}

// With one connection, Exclusive holds it and waits for the lock while
// Execute waits for the connection. Neither may hold the other's need.
func TestExclusiveMaxOpenOneNoDeadlock(t *testing.T) {
	e := newExec(t, executor.Config{MaxOpen: 1, MaxIdle: 1})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for range 8 {
		wg.Go(func() {
			for range 5 {
				if _, err := runQuery(ctx, e, "SELECT 1"); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	for range 3 {
		wg.Go(func() {
			if err := e.Exclusive(ctx, func(context.Context, *sql.Conn) error { return nil }); err != nil {
				errs <- err
			}
		})
	}
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("deadlock: calls did not finish within 10 s")
	}
	close(errs)
	for err := range errs {
		t.Errorf("call failed: %v", err)
	}
}

// A blocking aggregate holds the read lock until it is done. A waiting
// Exclusive must not make new queries wait behind it.
func TestExclusiveWaitDoesNotBlockNewExecute(t *testing.T) {
	e := newExec(t, executor.Config{MaxOpen: 4})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	longCtx, stopLong := context.WithCancel(ctx)
	defer stopLong()
	longDone := make(chan struct{})
	go func() {
		defer close(longDone)
		_, _ = runQuery(longCtx, e, "SELECT sum(range) FROM range(1000000000000)")
	}()
	time.Sleep(200 * time.Millisecond)

	exclDone := make(chan error, 1)
	go func() {
		exclDone <- e.Exclusive(ctx, func(context.Context, *sql.Conn) error { return nil })
	}()
	time.Sleep(200 * time.Millisecond)

	shortCtx, stopShort := context.WithTimeout(ctx, 3*time.Second)
	defer stopShort()
	if _, err := runQuery(shortCtx, e, "SELECT 1"); err != nil {
		t.Errorf("SELECT 1 while Exclusive waits: %v", err)
	}
	select {
	case <-longDone:
		t.Fatal("long query ended early; the test did not overlap")
	default:
	}

	stopLong()
	<-longDone
	if err := <-exclDone; err != nil {
		t.Errorf("Exclusive: %v", err)
	}
}
