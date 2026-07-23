// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/bino-bi/sluice/internal/audit"
)

func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// failingSink accepts the genesis record then errors on every subsequent
// Record — a secondary that goes down right after boot. (The real
// syslog/S3 sinks swallow delivery errors entirely; returning them here
// additionally exercises the dispatcher's per-record error isolation.)
type failingSink struct{ seen int }

func (f *failingSink) Name() string { return "failing" }
func (f *failingSink) Record(context.Context, *audit.Record) error {
	f.seen++
	if f.seen == 1 {
		return nil
	}
	return errors.New("secondary down")
}
func (*failingSink) Flush(context.Context) error { return nil }
func (*failingSink) Close(context.Context) error { return nil }

// TestDispatcher_SecondarySinkFailure_FileChainStillVerifies proves the
// fan-out isolation stance: a permanently failing secondary sink neither
// blocks the file sink nor breaks its hash chain.
func TestDispatcher_SecondarySinkFailure_FileChainStillVerifies(t *testing.T) {
	dir := t.TempDir()
	fs, err := audit.NewFileSink(audit.FileOptions{Dir: dir})
	if err != nil {
		t.Fatalf("file sink: %v", err)
	}
	seed := []byte("secondary-failure-seed")
	d, err := audit.NewDispatcher(audit.DispatcherOptions{
		Sinks:       []audit.Sink{fs, &failingSink{}},
		GenesisSeed: seed,
		Logger:      testLogger(t),
	})
	if err != nil {
		t.Fatalf("dispatcher: %v", err)
	}
	for i := range 5 {
		rec := &audit.Record{EventType: audit.EventQuery, Decision: audit.DecisionAllow}
		if err := d.Enqueue(context.Background(), rec); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	rep, err := audit.Verify(dir, audit.GenesisPriorHash(seed))
	if err != nil {
		t.Fatalf("chain must verify over the file sink: %v", err)
	}
	if rep.Records < 5 {
		t.Fatalf("records = %d, want >= 5", rep.Records)
	}
}
