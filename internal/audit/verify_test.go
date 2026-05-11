// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/audit"
)

func TestVerify_HappyPathAcrossRotations(t *testing.T) {
	dir := t.TempDir()

	// Write day 1.
	times := []time.Time{
		time.Date(2026, 4, 19, 23, 55, 0, 0, time.UTC),
		time.Date(2026, 4, 19, 23, 58, 0, 0, time.UTC),
		time.Date(2026, 4, 20, 0, 5, 0, 0, time.UTC),
		time.Date(2026, 4, 20, 0, 6, 0, 0, time.UTC),
	}
	idx := 0
	clock := func() time.Time {
		v := times[idx]
		if idx < len(times)-1 {
			idx++
		}
		return v
	}
	fs, err := audit.NewFileSink(audit.FileOptions{Dir: dir, Clock: clock})
	if err != nil {
		t.Fatalf("file sink: %v", err)
	}
	seed := []byte("verify-seed")
	d, err := audit.NewDispatcher(audit.DispatcherOptions{
		Sinks:       []audit.Sink{fs},
		GenesisSeed: seed,
		Clock:       clock,
	})
	if err != nil {
		t.Fatalf("dispatcher: %v", err)
	}
	// 2 records → one lands day 1 (the 23:58 clock tick), one crosses
	// into day 2 (00:05).
	for i := range 2 {
		rec := &audit.Record{EventType: audit.EventQuery}
		if err := d.Enqueue(context.Background(), rec); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	rep, err := audit.Verify(dir, audit.GenesisPriorHash(seed))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Files == 0 || rep.Records < 2 {
		t.Fatalf("unexpected report: %+v", rep)
	}
}

func TestVerify_DetectsTamperedRecord(t *testing.T) {
	dir := t.TempDir()
	fs, err := audit.NewFileSink(audit.FileOptions{Dir: dir})
	if err != nil {
		t.Fatalf("file sink: %v", err)
	}
	seed := []byte("tamper-seed")
	d, err := audit.NewDispatcher(audit.DispatcherOptions{
		Sinks:       []audit.Sink{fs},
		GenesisSeed: seed,
	})
	if err != nil {
		t.Fatalf("dispatcher: %v", err)
	}
	for i := range 3 {
		if err := d.Enqueue(context.Background(), &audit.Record{EventType: audit.EventQuery, QueryID: "q-" + itoa(i)}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	files, _ := filepath.Glob(filepath.Join(dir, "audit-*.jsonl"))
	if len(files) == 0 {
		t.Fatalf("no audit file written")
	}
	// Tamper with the second line: swap a character in its query_id.
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected ≥3 lines, got %d", len(lines))
	}
	lines[1] = strings.Replace(lines[1], `"q-0"`, `"q-X"`, 1)
	if err := os.WriteFile(files[0], []byte(strings.Join(lines, "\n")), 0o640); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err = audit.Verify(dir, audit.GenesisPriorHash(seed))
	if err == nil {
		t.Fatalf("verify should fail on tampered record")
	}
	if !errors.Is(err, audit.ErrChainBroken) {
		t.Fatalf("expected ErrChainBroken, got %v (%T)", err, err)
	}
	var ve *audit.VerifyError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *VerifyError, got %T", err)
	}
	if ve.Line != 2 {
		t.Fatalf("expected mismatch at line 2, got %d", ve.Line)
	}
}

func TestVerify_DetectsAnchorMismatch(t *testing.T) {
	dir := t.TempDir()
	fs, err := audit.NewFileSink(audit.FileOptions{Dir: dir})
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	d, err := audit.NewDispatcher(audit.DispatcherOptions{
		Sinks:       []audit.Sink{fs},
		GenesisSeed: []byte("right-seed"),
	})
	if err != nil {
		t.Fatalf("dispatcher: %v", err)
	}
	_ = d.Close(context.Background())

	// Wrong anchor → mismatch on first record.
	_, err = audit.Verify(dir, audit.GenesisPriorHash([]byte("wrong-seed")))
	if err == nil {
		t.Fatalf("expected anchor mismatch error")
	}
	if !errors.Is(err, audit.ErrChainBroken) {
		t.Fatalf("expected ErrChainBroken, got %v", err)
	}
}
