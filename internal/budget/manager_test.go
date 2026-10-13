// SPDX-License-Identifier: AGPL-3.0-or-later

package budget_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/bino-bi/sluice/internal/budget"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

func newManager(t *testing.T, path string) *budget.Manager {
	t.Helper()
	m, err := budget.New(budget.Options{
		Path:          path,
		FlushInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func TestBudget_RowsLimitEnforced(t *testing.T) {
	m := newManager(t, filepath.Join(t.TempDir(), "b.db"))
	defer func() { _ = m.Close(context.Background()) }()

	m.SetSpecs(map[string]budget.Spec{"alice": {RowsPerDay: 10}}, nil)

	// Under budget → allowed.
	if err := m.Check(context.Background(), "alice", ""); err != nil {
		t.Fatalf("Check under budget: %v", err)
	}
	m.Record("alice", "", time.Second, 10) // hits the cap

	err := m.Check(context.Background(), "alice", "")
	if ae := pkgerr.FromError(err); ae == nil || ae.Code != pkgerr.CodeBudgetExceeded {
		t.Fatalf("Check over budget = %v, want ERR_BUDGET_EXCEEDED", err)
	}
}

func TestBudget_CPULimitEnforced(t *testing.T) {
	m := newManager(t, filepath.Join(t.TempDir(), "b.db"))
	defer func() { _ = m.Close(context.Background()) }()
	m.SetSpecs(map[string]budget.Spec{"bob": {CPUSecondsPerDay: 2}}, nil)

	m.Record("bob", "", 2500*time.Millisecond, 1) // 2.5s > 2s cap
	if err := m.Check(context.Background(), "bob", ""); err == nil {
		t.Fatal("expected CPU budget exceeded")
	}
}

func TestBudget_UnbudgetedSubjectUnlimited(t *testing.T) {
	m := newManager(t, filepath.Join(t.TempDir(), "b.db"))
	defer func() { _ = m.Close(context.Background()) }()
	m.Record("nobody", "", time.Hour, 1_000_000)
	if err := m.Check(context.Background(), "nobody", ""); err != nil {
		t.Errorf("unbudgeted subject was limited: %v", err)
	}
}

func TestBudget_IssuerFallback(t *testing.T) {
	m := newManager(t, filepath.Join(t.TempDir(), "b.db"))
	defer func() { _ = m.Close(context.Background()) }()
	m.SetSpecs(nil, map[string]budget.Spec{"iss": {RowsPerDay: 5}})
	m.Record("anyone", "iss", 0, 5)
	if err := m.Check(context.Background(), "anyone", "iss"); err == nil {
		t.Error("issuer-level budget not enforced")
	}
}

func TestBudget_SurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "b.db")
	m1 := newManager(t, path)
	m1.SetSpecs(map[string]budget.Spec{"carol": {RowsPerDay: 100}}, nil)
	m1.Record("carol", "", time.Second, 60)
	// Force a flush by closing.
	if err := m1.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen — the 60 rows must be seeded back.
	m2 := newManager(t, path)
	defer func() { _ = m2.Close(context.Background()) }()
	m2.SetSpecs(map[string]budget.Spec{"carol": {RowsPerDay: 100}}, nil)
	// 60 more hits the 100 cap? 60+60=120 > 100.
	m2.Record("carol", "", 0, 60)
	if err := m2.Check(context.Background(), "carol", ""); err == nil {
		t.Error("budget not enforced against persisted+new usage")
	}
}

// ReadUsage is what `sluice budget show` runs. It must only read: no file
// for a wrong path, no row deleted, no counter written.
func TestReadUsage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "b.db")
	m := newManager(t, path)
	m.Record("carol", "", 2*time.Second, 60)
	if err := m.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// A row older than the retention time, which opening the manager would
	// delete.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO budget_usage (day, subject, issuer, cpu_ms, rows_served, updated_at) VALUES ('2020-01-01', 'carol', '', 5000, 7, '2020-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert old row: %v", err)
	}
	_ = db.Close()

	today, err := budget.ReadUsage(context.Background(), path, "carol", "")
	if err != nil || today.RowsPerDay != 60 || today.CPUSecondsPerDay != 2 {
		t.Errorf("today = %+v, %v; want 60 rows and 2 cpu seconds", today, err)
	}
	old, err := budget.ReadUsage(context.Background(), path, "carol", "2020-01-01")
	if err != nil || old.RowsPerDay != 7 || old.CPUSecondsPerDay != 5 {
		t.Errorf("old day = %+v, %v; want the row to be still there with 7 rows", old, err)
	}
	if again, _ := budget.ReadUsage(context.Background(), path, "carol", "2020-01-01"); again.RowsPerDay != 7 {
		t.Error("a read deleted the old row")
	}

	missing := filepath.Join(dir, "nope", "budget.db")
	if _, err := budget.ReadUsage(context.Background(), missing, "carol", ""); err == nil {
		t.Error("a missing store gave no error")
	}
	if _, err := os.Stat(filepath.Dir(missing)); !os.IsNotExist(err) {
		t.Error("a read created the state directory")
	}
}
