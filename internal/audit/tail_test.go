// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/audit"
)

// tailSink writes five chained records across a forced daily rotation:
// q-1, q-2 land in 2026-04-19, q-3..q-5 in 2026-04-20.
func tailSink(t *testing.T) (*audit.FileSink, string) {
	t.Helper()
	dir := t.TempDir()
	times := []time.Time{
		time.Date(2026, 4, 19, 23, 50, 0, 0, time.UTC), // openLatest
		time.Date(2026, 4, 19, 23, 55, 0, 0, time.UTC), // q-1
		time.Date(2026, 4, 19, 23, 58, 0, 0, time.UTC), // q-2
		time.Date(2026, 4, 20, 0, 5, 0, 0, time.UTC),   // q-3 (rotates)
		time.Date(2026, 4, 20, 0, 6, 0, 0, time.UTC),   // q-4
		time.Date(2026, 4, 20, 0, 7, 0, 0, time.UTC),   // q-5
	}
	idx := 0
	clock := func() time.Time {
		v := times[idx]
		if idx < len(times)-1 {
			idx++
		}
		return v
	}
	sink, err := audit.NewFileSink(audit.FileOptions{Dir: dir, RotateDaily: true, Clock: clock})
	if err != nil {
		t.Fatalf("open sink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close(context.Background()) })

	prior := audit.GenesisPriorHash([]byte("seed"))
	for _, qid := range []string{"q-1", "q-2", "q-3", "q-4", "q-5"} {
		r := chain(t, prior, func(r *audit.Record) { r.QueryID = qid })
		if err := sink.Record(context.Background(), r); err != nil {
			t.Fatalf("record %s: %v", qid, err)
		}
		prior = r.Hash
	}
	// Deliberately no Flush: Tail must flush buffered writes itself.
	return sink, dir
}

func queryIDs(recs []*audit.Record) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.QueryID
	}
	return out
}

func TestTail_LastNAcrossRotatedFiles(t *testing.T) {
	sink, _ := tailSink(t)
	recs, err := sink.Tail(context.Background(), 4)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	got := queryIDs(recs)
	want := []string{"q-2", "q-3", "q-4", "q-5"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order mismatch: got %v want %v", got, want)
		}
	}
}

func TestTail_MoreThanAvailableReturnsAll(t *testing.T) {
	sink, _ := tailSink(t)
	recs, err := sink.Tail(context.Background(), 100)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(recs) != 5 || recs[0].QueryID != "q-1" || recs[4].QueryID != "q-5" {
		t.Fatalf("got %v", queryIDs(recs))
	}
}

func TestTail_ZeroAndNegativeN(t *testing.T) {
	sink, _ := tailSink(t)
	for _, n := range []int{0, -3} {
		recs, err := sink.Tail(context.Background(), n)
		if err != nil || recs != nil {
			t.Fatalf("Tail(%d) = %v, %v; want nil, nil", n, recs, err)
		}
	}
}

func TestTail_ClosedSink(t *testing.T) {
	sink, _ := tailSink(t)
	if err := sink.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := sink.Tail(context.Background(), 1); err == nil {
		t.Fatal("expected error on closed sink")
	}
}

func TestTail_PartialTrailingLineSkipped(t *testing.T) {
	sink, dir := tailSink(t)
	if err := sink.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	// Simulate a concurrent half-written append on the newest file.
	newest := filepath.Join(dir, "audit-2026-04-20.jsonl")
	f, err := os.OpenFile(newest, os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		t.Fatalf("open newest: %v", err)
	}
	if _, err := f.WriteString(`{"timestamp":"2026-04-2`); err != nil {
		t.Fatalf("append partial: %v", err)
	}
	_ = f.Close()

	recs, err := sink.Tail(context.Background(), 10)
	if err != nil {
		t.Fatalf("Tail with partial trailing line: %v", err)
	}
	if len(recs) != 5 || recs[4].QueryID != "q-5" {
		t.Fatalf("got %v", queryIDs(recs))
	}
}

func TestTail_MalformedMidFileLineErrors(t *testing.T) {
	sink, dir := tailSink(t)
	if err := sink.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	// Corruption in an older (non-newest) file must surface, not be
	// silently skipped.
	older := filepath.Join(dir, "audit-2026-04-19.jsonl")
	raw, err := os.ReadFile(older)
	if err != nil {
		t.Fatalf("read older: %v", err)
	}
	if err := os.WriteFile(older, append([]byte("garbage-line\n"), raw...), 0o640); err != nil {
		t.Fatalf("rewrite older: %v", err)
	}
	if _, err := sink.Tail(context.Background(), 10); err == nil {
		t.Fatal("expected error for malformed mid-file line")
	}
}
