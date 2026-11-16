// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A failed write reopens the file and writes the record once more.
func TestFileSink_WriteErrorReopens(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	s, err := NewFileSink(FileOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(context.Background()) }()

	r := &Record{EventType: EventQuery, PriorHash: "p"}
	h, err := ComputeHash(r.PriorHash, r)
	if err != nil {
		t.Fatal(err)
	}
	r.Hash = h
	// A closed descriptor fails the next write, as a dead disk would.
	_ = s.file.Close()
	if err := s.Record(context.Background(), r); err != nil {
		t.Fatalf("record after a failed write: %v", err)
	}
	if s.LastHash() != r.Hash {
		t.Errorf("LastHash = %q, want the record", s.LastHash())
	}
	files, _ := filepath.Glob(filepath.Join(dir, "audit-*.jsonl"))
	data, _ := os.ReadFile(files[0])
	if last, _ := readLastRecord(files[0]); last == nil || last.Hash != r.Hash || data[len(data)-1] != '\n' {
		t.Errorf("file = %q, want the record as the last line", data)
	}
}

// tornTail finds the last newline across read chunks.
func TestTornTail(t *testing.T) {
	big := strings.Repeat("x", 200*1024)
	for _, tc := range []struct {
		data     string
		keep     int64
		fragment string
	}{
		{"", 0, ""},
		{"a\n", 2, ""},
		{"a\nb", 2, "b"},
		{"partial", 0, "partial"},
		{"a\n" + big, 2, big},
		{big + "\n" + "c", int64(len(big)) + 1, "c"},
	} {
		path := filepath.Join(t.TempDir(), "f")
		if err := os.WriteFile(path, []byte(tc.data), 0o600); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		keep, frag, err := tornTail(f)
		_ = f.Close()
		if err != nil || keep != tc.keep || string(frag) != tc.fragment {
			t.Errorf("tornTail(%d bytes) = %d, %d bytes, %v; want %d, %d bytes", len(tc.data), keep, len(frag), err, tc.keep, len(tc.fragment))
		}
	}
}

// When the side file cannot be written, the log keeps its tail.
func TestRepairTornTail_SideFileFails(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s := &FileSink{fileMode: 0o640, clock: func() time.Time { return now }}
	path := filepath.Join(dir, "audit-2026-01-02.jsonl")
	if err := os.WriteFile(path, []byte("{}\n{\"torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A directory in its place: the side file cannot be created.
	side := path + ".torn-" + now.Format("20060102T150405.000000000Z")
	if err := os.Mkdir(side, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.repairTornTail(path); err == nil {
		t.Fatal("repair worked although the side file cannot be written")
	}
	if got, _ := os.ReadFile(path); string(got) != "{}\n{\"torn" {
		t.Errorf("log = %q, want it unchanged", got)
	}
}
