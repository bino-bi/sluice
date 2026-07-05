// SPDX-License-Identifier: AGPL-3.0-or-later

// Package budget enforces per-subject daily query budgets (CPU-seconds +
// rows) backed by an embedded SQLite store. Counters are authoritative in
// memory for the current UTC day and flushed to SQLite asynchronously so a
// restart resumes near where it left off.
package budget

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver

	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// Spec is a per-subject (or per-issuer) daily budget. A zero field means
// "no limit on that dimension".
type Spec struct {
	CPUSecondsPerDay int64
	RowsPerDay       int64
}

// Options configures a Manager.
type Options struct {
	Path          string // SQLite file path; required
	FlushInterval time.Duration
	RetentionDays int
	Clock         func() time.Time
	Logger        *slog.Logger
}

// usage accumulates one subject's current-day counters.
type usage struct {
	cpuMS int64
	rows  int64
}

// Manager tracks and enforces budgets.
type Manager struct {
	db            *sql.DB
	flushInterval time.Duration
	retentionDays int
	clock         func() time.Time
	logger        *slog.Logger

	mu       sync.Mutex
	day      string // current UTC day (YYYY-MM-DD)
	counters map[string]*usage
	specs    struct {
		bySubject map[string]Spec
		byIssuer  map[string]Spec
	}
	closed chan struct{}
	wg     sync.WaitGroup
}

// New opens the store, seeds today's counters, and starts the flusher.
func New(opts Options) (*Manager, error) {
	if opts.Path == "" {
		return nil, fmt.Errorf("budget: Path is required")
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = 5 * time.Second
	}
	if opts.RetentionDays <= 0 {
		opts.RetentionDays = 35
	}
	db, err := sql.Open("sqlite", opts.Path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("budget: open store: %w", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS budget_usage (
		day TEXT NOT NULL, subject TEXT NOT NULL, issuer TEXT NOT NULL,
		cpu_ms INTEGER NOT NULL, rows_served INTEGER NOT NULL, updated_at TEXT NOT NULL,
		PRIMARY KEY (day, subject, issuer))`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("budget: create schema: %w", err)
	}

	m := &Manager{
		db:            db,
		flushInterval: opts.FlushInterval,
		retentionDays: opts.RetentionDays,
		clock:         opts.Clock,
		logger:        opts.Logger,
		counters:      map[string]*usage{},
		closed:        make(chan struct{}),
	}
	m.specs.bySubject = map[string]Spec{}
	m.specs.byIssuer = map[string]Spec{}
	m.day = m.today()
	if err := m.seed(); err != nil {
		_ = db.Close()
		return nil, err
	}
	m.gc()
	m.wg.Add(1)
	go m.flushLoop()
	return m, nil
}

// SetSpecs replaces the budget specs (called at boot and on reload).
func (m *Manager) SetSpecs(bySubject, byIssuer map[string]Spec) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.specs.bySubject = bySubject
	m.specs.byIssuer = byIssuer
}

// Check returns an ERR_BUDGET_EXCEEDED APIError when the subject has
// exhausted either dimension of its daily budget. A subject with no spec
// is unlimited.
func (m *Manager) Check(_ context.Context, subject, issuer string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rollDayLocked()
	spec, ok := m.specFor(subject, issuer)
	if !ok {
		return nil
	}
	u := m.counters[key(subject, issuer)]
	if u == nil {
		return nil
	}
	if spec.CPUSecondsPerDay > 0 && u.cpuMS >= spec.CPUSecondsPerDay*1000 {
		return pkgerr.New(pkgerr.CodeBudgetExceeded).WithMessage("daily CPU budget exhausted")
	}
	if spec.RowsPerDay > 0 && u.rows >= spec.RowsPerDay {
		return pkgerr.New(pkgerr.CodeBudgetExceeded).WithMessage("daily row budget exhausted")
	}
	return nil
}

// Record accumulates usage after a query executes.
func (m *Manager) Record(subject, issuer string, cpu time.Duration, rows int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rollDayLocked()
	k := key(subject, issuer)
	u := m.counters[k]
	if u == nil {
		u = &usage{}
		m.counters[k] = u
	}
	u.cpuMS += cpu.Milliseconds()
	u.rows += rows
}

// Usage reports a subject's usage for a given UTC day (default today). For
// today the authoritative in-memory counters are returned (the DB is only a
// backup that flush mirrors); for past days the persisted rows are summed.
func (m *Manager) Usage(_ context.Context, subject, day string) (Spec, error) {
	if day == "" {
		day = m.today()
	}
	if day == m.today() {
		var cpuMS, rows int64
		m.mu.Lock()
		for k, u := range m.counters {
			if subjectOf(k) == subject {
				cpuMS += u.cpuMS
				rows += u.rows
			}
		}
		m.mu.Unlock()
		return Spec{CPUSecondsPerDay: cpuMS / 1000, RowsPerDay: rows}, nil
	}
	var cpuMS, rows int64
	err := m.db.QueryRow(
		`SELECT COALESCE(SUM(cpu_ms),0), COALESCE(SUM(rows_served),0) FROM budget_usage WHERE day=? AND subject=?`,
		day, subject).Scan(&cpuMS, &rows)
	if err != nil {
		return Spec{}, err
	}
	return Spec{CPUSecondsPerDay: cpuMS / 1000, RowsPerDay: rows}, nil
}

// Close flushes and closes the store.
func (m *Manager) Close(_ context.Context) error {
	select {
	case <-m.closed:
	default:
		close(m.closed)
	}
	m.wg.Wait()
	m.flush()
	return m.db.Close()
}

// --- internals ---

func (m *Manager) specFor(subject, issuer string) (Spec, bool) {
	if s, ok := m.specs.bySubject[subject]; ok {
		return s, true
	}
	if s, ok := m.specs.byIssuer[issuer]; ok {
		return s, true
	}
	return Spec{}, false
}

func (m *Manager) today() string { return m.clock().UTC().Format("2006-01-02") }

// rollDayLocked resets the in-memory counters when the UTC day changes.
func (m *Manager) rollDayLocked() {
	today := m.today()
	if today == m.day {
		return
	}
	m.day = today
	m.counters = map[string]*usage{}
}

// seed loads today's persisted counters into memory.
func (m *Manager) seed() error {
	rows, err := m.db.Query(`SELECT subject, issuer, cpu_ms, rows_served FROM budget_usage WHERE day=?`, m.day)
	if err != nil {
		return fmt.Errorf("budget: seed: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var subject, issuer string
		var cpuMS, served int64
		if err := rows.Scan(&subject, &issuer, &cpuMS, &served); err != nil {
			return err
		}
		m.counters[key(subject, issuer)] = &usage{cpuMS: cpuMS, rows: served}
	}
	return rows.Err()
}

func (m *Manager) flushLoop() {
	defer m.wg.Done()
	t := time.NewTicker(m.flushInterval)
	defer t.Stop()
	for {
		select {
		case <-m.closed:
			return
		case <-t.C:
			m.flush()
		}
	}
}

// flush writes the current in-memory counters to SQLite via upsert.
func (m *Manager) flush() {
	m.mu.Lock()
	day := m.day
	snapshot := make(map[string]usage, len(m.counters))
	for k, u := range m.counters {
		snapshot[k] = *u
	}
	m.mu.Unlock()

	if len(snapshot) == 0 {
		return
	}
	now := m.clock().UTC().Format(time.RFC3339)
	tx, err := m.db.Begin()
	if err != nil {
		m.logger.Error("budget: flush begin failed", slog.String("error", err.Error()))
		return
	}
	for k, u := range snapshot {
		subject, issuer := splitKey(k)
		if _, err := tx.Exec(
			`INSERT INTO budget_usage (day, subject, issuer, cpu_ms, rows_served, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)
			 ON CONFLICT(day, subject, issuer) DO UPDATE SET
			   cpu_ms=excluded.cpu_ms, rows_served=excluded.rows_served, updated_at=excluded.updated_at`,
			day, subject, issuer, u.cpuMS, u.rows, now); err != nil {
			_ = tx.Rollback()
			m.logger.Error("budget: flush upsert failed", slog.String("error", err.Error()))
			return
		}
	}
	if err := tx.Commit(); err != nil {
		m.logger.Error("budget: flush commit failed", slog.String("error", err.Error()))
	}
}

// gc deletes usage rows older than the retention window.
func (m *Manager) gc() {
	cutoff := m.clock().UTC().AddDate(0, 0, -m.retentionDays).Format("2006-01-02")
	if _, err := m.db.Exec(`DELETE FROM budget_usage WHERE day < ?`, cutoff); err != nil {
		m.logger.Warn("budget: gc failed", slog.String("error", err.Error()))
	}
}
