// SPDX-License-Identifier: AGPL-3.0-or-later

package budget_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

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
	u, err := m2.Usage(context.Background(), "carol", "")
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if u.RowsPerDay != 60 {
		t.Errorf("rows after restart = %d, want 60 (persisted)", u.RowsPerDay)
	}
	// 60 more hits the 100 cap? 60+60=120 > 100.
	m2.Record("carol", "", 0, 60)
	if err := m2.Check(context.Background(), "carol", ""); err == nil {
		t.Error("budget not enforced against persisted+new usage")
	}
}
