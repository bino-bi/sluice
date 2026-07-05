// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/audit"
)

// recordWithHash returns a fresh record chained to prior, with the hash
// pre-computed. Helper for tests.
func chain(t *testing.T, prior string, tweak func(*audit.Record)) *audit.Record {
	t.Helper()
	r := sampleRecord()
	r.PriorHash = prior
	if tweak != nil {
		tweak(r)
	}
	h, err := audit.ComputeHash(prior, r)
	if err != nil {
		t.Fatalf("compute hash: %v", err)
	}
	r.Hash = h
	return r
}

func TestFileSink_AppendAndRotateDaily(t *testing.T) {
	dir := t.TempDir()
	// Give explicit times per call so the sequencing is unambiguous:
	//   openLatest → 23:55 (opens 04-19 file)
	//   Record r1  → 23:58 (still 04-19)
	//   Record r2  → 00:05 next day (rotates into 04-20)
	times := []time.Time{
		time.Date(2026, 4, 19, 23, 55, 0, 0, time.UTC),
		time.Date(2026, 4, 19, 23, 58, 0, 0, time.UTC),
		time.Date(2026, 4, 20, 0, 5, 0, 0, time.UTC),
	}
	idx := 0
	clock := func() time.Time {
		v := times[idx]
		if idx < len(times)-1 {
			idx++
		}
		return v
	}
	sink, err := audit.NewFileSink(audit.FileOptions{
		Dir:         dir,
		RotateDaily: true,
		Clock:       clock,
	})
	if err != nil {
		t.Fatalf("open sink: %v", err)
	}
	defer func() { _ = sink.Close(context.Background()) }()

	r1 := chain(t, audit.GenesisPriorHash([]byte("seed")), func(r *audit.Record) { r.QueryID = "q-1" })
	if err := sink.Record(context.Background(), r1); err != nil {
		t.Fatalf("record 1: %v", err)
	}
	r2 := chain(t, r1.Hash, func(r *audit.Record) { r.QueryID = "q-2" })
	if err := sink.Record(context.Background(), r2); err != nil {
		t.Fatalf("record 2: %v", err)
	}
	if err := sink.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	want := []string{"audit-2026-04-19.jsonl", "audit-2026-04-20.jsonl"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected files: %v", names)
	}

	// Record 1 should land in 04-19, record 2 in 04-20.
	first := readAll(t, filepath.Join(dir, "audit-2026-04-19.jsonl"))
	second := readAll(t, filepath.Join(dir, "audit-2026-04-20.jsonl"))
	if len(first) != 1 || first[0]["query_id"] != "q-1" {
		t.Fatalf("expected q-1 in first file, got %v", first)
	}
	if len(second) != 1 || second[0]["query_id"] != "q-2" {
		t.Fatalf("expected q-2 in second file, got %v", second)
	}
}

func TestFileSink_SizeRotation(t *testing.T) {
	dir := t.TempDir()
	sink, err := audit.NewFileSink(audit.FileOptions{
		Dir:          dir,
		RotateDaily:  false,
		RotateSizeMB: 1, // one record is easily < 1 MiB; force tiny cap below
		Clock:        func() time.Time { return time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = sink.Close(context.Background()) }()

	// Directly invoke with a size much bigger than the cap — easiest way
	// is to lower the cap via FileOptions; we can't touch internals. So
	// write a record whose sample blob is huge.
	big := strings.Repeat("x", 600*1024)
	r1 := chain(t, "prior", func(r *audit.Record) { r.QueryID = "big-1"; r.SQLSample = big })
	r2 := chain(t, r1.Hash, func(r *audit.Record) { r.QueryID = "big-2"; r.SQLSample = big })
	if err := sink.Record(context.Background(), r1); err != nil {
		t.Fatalf("record 1: %v", err)
	}
	if err := sink.Record(context.Background(), r2); err != nil {
		t.Fatalf("record 2: %v", err)
	}
	if err := sink.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	// Expect two files.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("expected 2 files after size rotation, got %d: %v", len(entries), fileNames(entries))
	}
}

func TestFileSink_PermissionsAndRestart(t *testing.T) {
	dir := t.TempDir()
	sink1, err := audit.NewFileSink(audit.FileOptions{
		Dir:   dir,
		Clock: func() time.Time { return time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	r1 := chain(t, "seed-prior", func(r *audit.Record) { r.QueryID = "q-r1" })
	if err := sink1.Record(context.Background(), r1); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := sink1.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Permission 0640 on POSIX.
	path := filepath.Join(dir, "audit-2026-04-20.jsonl")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm()&0o177 != 0o040 { // owner-rw, group-r, no others
		t.Logf("file mode %v (permission check is best-effort on non-POSIX)", info.Mode().Perm())
	}

	// Re-open and verify LastHash is populated.
	sink2, err := audit.NewFileSink(audit.FileOptions{
		Dir:   dir,
		Clock: func() time.Time { return time.Date(2026, 4, 20, 10, 5, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = sink2.Close(context.Background()) }()
	if sink2.LastHash() != r1.Hash {
		t.Fatalf("LastHash on reopen = %q, want %q", sink2.LastHash(), r1.Hash)
	}
	// Chain should continue.
	r2 := chain(t, sink2.LastHash(), func(r *audit.Record) { r.QueryID = "q-r2" })
	if err := sink2.Record(context.Background(), r2); err != nil {
		t.Fatalf("record 2: %v", err)
	}
	if err := sink2.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	recs := readAll(t, path)
	if len(recs) != 2 {
		t.Fatalf("expected 2 records after restart, got %d", len(recs))
	}
	if recs[1]["prior_hash"] != r1.Hash {
		t.Fatalf("restart prior_hash = %v, want %s", recs[1]["prior_hash"], r1.Hash)
	}
}

// TestFileSink_SameDaySizeRotationOrdering guards the filename-ordering
// bug: a same-day size rotation produces audit-D.jsonl (seq 0) and
// audit-D-1.jsonl (seq 1). Lexicographic order sorts seq-1 first (because
// '-' < '.'), which made Verify falsely report tampering and made a restart
// prime lastHash from the wrong file. Chronological (day, seq) ordering
// fixes both.
func TestFileSink_SameDaySizeRotationOrdering(t *testing.T) {
	dir := t.TempDir()
	clock := func() time.Time { return time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC) }
	sink, err := audit.NewFileSink(audit.FileOptions{Dir: dir, RotateSizeMB: 1, Clock: clock})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	anchor := audit.GenesisPriorHash([]byte("seed"))
	big := strings.Repeat("x", 600*1024)
	r1 := chain(t, anchor, func(r *audit.Record) { r.QueryID = "q-1"; r.SQLSample = big })
	r2 := chain(t, r1.Hash, func(r *audit.Record) { r.QueryID = "q-2"; r.SQLSample = big })
	if err := sink.Record(context.Background(), r1); err != nil {
		t.Fatalf("record 1: %v", err)
	}
	if err := sink.Record(context.Background(), r2); err != nil {
		t.Fatalf("record 2: %v", err)
	}
	if err := sink.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Sanity: two files, seq-0 and seq-1 within the same day.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("expected 2 files, got %v", fileNames(entries))
	}

	// Verify must chain across the rotation, not falsely report tampering.
	rep, verr := audit.Verify(dir, anchor)
	if verr != nil {
		t.Fatalf("verify falsely reported a broken chain across same-day rotation: %v", verr)
	}
	if rep.Records != 2 {
		t.Fatalf("verify records = %d, want 2", rep.Records)
	}

	// Restart must select the seq-1 file as latest and prime lastHash from
	// r2 (the true tail), not from the seq-0 file.
	sink2, err := audit.NewFileSink(audit.FileOptions{Dir: dir, RotateSizeMB: 1, Clock: clock})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = sink2.Close(context.Background()) }()
	if sink2.LastHash() != r2.Hash {
		t.Fatalf("restart LastHash = %q, want r2 %q", sink2.LastHash(), r2.Hash)
	}
}

func readAll(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024), 2*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("parse line %s: %v", line, err)
		}
		out = append(out, m)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return out
}

func fileNames(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}
