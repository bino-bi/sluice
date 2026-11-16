// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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
