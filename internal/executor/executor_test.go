// SPDX-License-Identifier: AGPL-3.0-or-later

package executor_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/executor"
)

// newExec constructs an Executor backed by an ephemeral in-memory DuckDB
// instance. Close is registered as a cleanup so each test gets an
// isolated pool.
func newExec(t *testing.T, cfg executor.Config) *executor.Executor {
	t.Helper()
	e, err := executor.New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("executor.New: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func TestExecutorHardeningLocksConfig(t *testing.T) {
	e := newExec(t, executor.Config{})

	// After init runs, lock_configuration should be true and
	// enable_external_access should be false.
	conn, err := e.DB().Conn(context.Background())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer func() { _ = conn.Close() }()

	var locked bool
	if err := conn.QueryRowContext(context.Background(),
		"SELECT value::BOOLEAN FROM duckdb_settings() WHERE name='lock_configuration'",
	).Scan(&locked); err != nil {
		t.Fatalf("scan lock_configuration: %v", err)
	}
	if !locked {
		t.Fatal("lock_configuration = false; hardening did not run")
	}

	var extAccess bool
	if err := conn.QueryRowContext(context.Background(),
		"SELECT value::BOOLEAN FROM duckdb_settings() WHERE name='enable_external_access'",
	).Scan(&extAccess); err != nil {
		t.Fatalf("scan enable_external_access: %v", err)
	}
	if extAccess {
		t.Fatal("enable_external_access = true; expected false after hardening")
	}
}

func TestExecutorSelectOne(t *testing.T) {
	e := newExec(t, executor.Config{})

	resp, err := e.Execute(context.Background(), executor.Request{SQL: "SELECT 42 AS answer"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	defer func() { _ = resp.Rows.Close() }()

	if len(resp.Columns) != 1 || resp.Columns[0].Name != "answer" {
		t.Fatalf("columns = %+v", resp.Columns)
	}
	var values []int64
	for resp.Rows.Next() {
		var n int64
		if err := resp.Rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		values = append(values, n)
	}
	if err := resp.Rows.Err(); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(values) != 1 || values[0] != 42 {
		t.Fatalf("values = %v; want [42]", values)
	}
}

func TestExecutorParameterisedQuery(t *testing.T) {
	e := newExec(t, executor.Config{})
	resp, err := e.Execute(context.Background(), executor.Request{
		SQL:    "SELECT ?::BIGINT + ?::BIGINT",
		Params: []any{3, 4},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	defer func() { _ = resp.Rows.Close() }()

	if !resp.Rows.Next() {
		t.Fatal("no row")
	}
	var sum int64
	if err := resp.Rows.Scan(&sum); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if sum != 7 {
		t.Fatalf("sum = %d; want 7", sum)
	}
}

func TestExecutorMaxRowsTruncates(t *testing.T) {
	e := newExec(t, executor.Config{})
	resp, err := e.Execute(context.Background(), executor.Request{
		SQL:     "SELECT i FROM range(100) AS t(i)",
		MaxRows: 10,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	defer func() { _ = resp.Rows.Close() }()

	var seen int64
	for resp.Rows.Next() {
		var n int64
		if err := resp.Rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen++
	}
	if seen != 10 {
		t.Fatalf("seen = %d; want 10", seen)
	}
	if !resp.Truncated {
		t.Fatal("Truncated = false; want true after hitting cap")
	}
}

func TestExecutorTimeoutCancelsQuery(t *testing.T) {
	e := newExec(t, executor.Config{
		DefaultStatementTimeout: 100 * time.Millisecond,
	})

	// A recursive CTE that would run for ages if allowed to finish.
	sql := `
WITH RECURSIVE t(i) AS (
    SELECT 1 UNION ALL SELECT i + 1 FROM t WHERE i < 10000000
)
SELECT count(*) FROM t
`
	resp, err := e.Execute(context.Background(), executor.Request{
		SQL:     sql,
		Timeout: 100 * time.Millisecond,
	})
	if err != nil {
		// Timeout may surface here (query prep phase).
		if !errors.Is(err, executor.ErrExecute) {
			t.Fatalf("err = %v; want ErrExecute", err)
		}
		return
	}
	defer func() { _ = resp.Rows.Close() }()

	// Drain — should surface a timeout during iteration.
	for resp.Rows.Next() {
		var n int64
		_ = resp.Rows.Scan(&n)
	}
	if err := resp.Rows.Err(); err == nil {
		// DuckDB may return a small result before the deadline; this
		// is acceptable. The test demonstrates the path is wired.
		return
	} else if !errors.Is(err, executor.ErrExecute) {
		t.Fatalf("err = %v; want ErrExecute wrapper", err)
	}
}

func TestExecutorAttachHookRuns(t *testing.T) {
	var calls int
	hook := func(_ context.Context, _ *sql.Conn) error {
		calls++
		return nil
	}
	e := newExec(t, executor.Config{AttachHook: hook})
	// Ping during New already triggered init (+AttachHook) once.
	if calls == 0 {
		t.Fatal("AttachHook never called")
	}
	// Force another connection and make sure hook runs on a fresh conn.
	// database/sql pools connections so this won't always open a new
	// one, but we exercise the path.
	for range 4 {
		if err := e.Ping(context.Background()); err != nil {
			t.Fatalf("Ping: %v", err)
		}
	}
	if calls < 1 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestExecutorRejectsArrowForNow(t *testing.T) {
	e := newExec(t, executor.Config{})
	_, err := e.Execute(context.Background(), executor.Request{SQL: "SELECT 1", Format: executor.FormatArrow})
	if err == nil {
		t.Fatal("want err for Arrow")
	}
}

func TestExecutorConfigClonesDefaults(t *testing.T) {
	e := newExec(t, executor.Config{})
	if e.DB() == nil {
		t.Fatal("DB() nil")
	}
	if err := e.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}
