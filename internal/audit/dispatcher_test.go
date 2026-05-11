// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/audit"
)

// memSink is an in-memory sink used to exercise the dispatcher without
// touching disk.
type memSink struct {
	mu        sync.Mutex
	records   []*audit.Record
	lastHash  string
	recordErr error
	latency   time.Duration
}

func (m *memSink) Name() string { return "mem" }
func (m *memSink) Record(_ context.Context, r *audit.Record) error {
	if m.latency > 0 {
		time.Sleep(m.latency)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recordErr != nil {
		return m.recordErr
	}
	m.records = append(m.records, r)
	m.lastHash = r.Hash
	return nil
}
func (m *memSink) Flush(_ context.Context) error { return nil }
func (m *memSink) Close(_ context.Context) error { return nil }
func (m *memSink) LastHash() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastHash
}
func (m *memSink) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.records)
}

func TestDispatcher_EnqueuesAndChains(t *testing.T) {
	sink := &memSink{}
	d, err := audit.NewDispatcher(audit.DispatcherOptions{
		Sinks:       []audit.Sink{sink},
		GenesisSeed: []byte("seed-chain"),
		QueueSize:   16,
	})
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}
	defer func() { _ = d.Close(context.Background()) }()

	// Enqueue 10 records; Flush; verify count + chain integrity.
	for i := range 10 {
		rec := &audit.Record{EventType: audit.EventQuery, Decision: audit.DecisionAllow}
		if err := d.Enqueue(context.Background(), rec); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if err := d.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := sink.count(); got != 11 { // 10 + genesis
		t.Fatalf("sink count = %d, want 11", got)
	}
	// Chain integrity.
	prior := sink.records[0].PriorHash
	for i, r := range sink.records {
		want := prior
		if r.PriorHash != want {
			t.Fatalf("record %d prior_hash = %q, want %q", i, r.PriorHash, want)
		}
		h, err := audit.ComputeHash(r.PriorHash, r)
		if err != nil {
			t.Fatalf("hash %d: %v", i, err)
		}
		if h != r.Hash {
			t.Fatalf("record %d hash mismatch: got %s, recomputed %s", i, r.Hash, h)
		}
		prior = r.Hash
	}
}

func TestDispatcher_BackpressureAndDrop(t *testing.T) {
	sink := &memSink{latency: 20 * time.Millisecond}
	d, err := audit.NewDispatcher(audit.DispatcherOptions{
		Sinks:           []audit.Sink{sink},
		GenesisSeed:     []byte("bp"),
		QueueSize:       2,
		EnqueueDeadline: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}
	defer func() { _ = d.Close(context.Background()) }()

	// Flood with small bursts so the worker can't keep up; at least one
	// call should observe ErrQueueFull.
	var (
		fullCount int
		okCount   int
	)
	for range 30 {
		err := d.Enqueue(context.Background(), &audit.Record{EventType: audit.EventQuery})
		switch {
		case err == nil:
			okCount++
		case errors.Is(err, audit.ErrQueueFull):
			fullCount++
		default:
			t.Fatalf("unexpected err: %v", err)
		}
	}
	if fullCount == 0 {
		t.Fatalf("expected at least one ErrQueueFull, got %d ok / %d full", okCount, fullCount)
	}
}

func TestDispatcher_CloseIsIdempotent(t *testing.T) {
	sink := &memSink{}
	d, err := audit.NewDispatcher(audit.DispatcherOptions{
		Sinks:       []audit.Sink{sink},
		GenesisSeed: []byte("close"),
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatalf("close 1: %v", err)
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatalf("close 2 (idempotent): %v", err)
	}
	err = d.Enqueue(context.Background(), &audit.Record{EventType: audit.EventQuery})
	if !errors.Is(err, audit.ErrClosed) {
		t.Fatalf("expected ErrClosed, got %v", err)
	}
}

func TestDispatcher_FileSinkIntegration(t *testing.T) {
	dir := t.TempDir()
	fs, err := audit.NewFileSink(audit.FileOptions{Dir: dir})
	if err != nil {
		t.Fatalf("file sink: %v", err)
	}
	d, err := audit.NewDispatcher(audit.DispatcherOptions{
		Sinks:       []audit.Sink{fs},
		GenesisSeed: []byte("integration"),
		QueueSize:   32,
	})
	if err != nil {
		t.Fatalf("dispatcher: %v", err)
	}
	for i := range 5 {
		rec := &audit.Record{EventType: audit.EventQuery, QueryID: QueueID("q", i)}
		if err := d.Enqueue(context.Background(), rec); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Spot-check the file exists and contains 6 lines (genesis + 5).
	matches, _ := filepath.Glob(filepath.Join(dir, "audit-*.jsonl"))
	if len(matches) == 0 {
		t.Fatalf("no audit file written")
	}
	recs := readAll(t, matches[0])
	if len(recs) != 6 {
		t.Fatalf("expected 6 lines (genesis + 5), got %d", len(recs))
	}
}

// QueueID helper so the test above can one-line inline assigns.
func QueueID(prefix string, i int) string { return prefix + "-" + itoa(i) }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [4]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[n:])
}
