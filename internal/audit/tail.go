// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Tail returns up to n of the most recent records in chronological order
// (oldest → newest), reading rotated files newest-first. Buffered writes
// are flushed first so just-recorded entries are visible; a partial
// trailing line in the newest file (a write racing this read) is
// skipped. A malformed line anywhere else is reported as an error —
// that is chain corruption, not a race. n <= 0 returns nil.
func (s *FileSink) Tail(ctx context.Context, n int) ([]*Record, error) {
	if n <= 0 {
		return nil, nil
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	if s.writer != nil {
		if err := s.writer.Flush(); err != nil {
			s.mu.Unlock()
			return nil, fmt.Errorf("audit: tail flush: %w", err)
		}
	}
	dir, prefix := s.dir, s.prefix
	s.mu.Unlock()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("audit: tail readdir %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".jsonl") {
			names = append(names, name)
		}
	}
	sortAuditNames(names)

	// Walk newest-first, prepending each file's tail until n records are
	// collected. Files are bounded by daily/size rotation, so scanning a
	// whole file to keep its tail is fine.
	var out []*Record
	for i := len(names) - 1; i >= 0 && len(out) < n; i-- {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		newest := i == len(names)-1
		recs, err := readTailRecords(filepath.Join(dir, names[i]), names[i], n-len(out), newest)
		if err != nil {
			return nil, err
		}
		out = append(recs, out...)
	}
	return out, nil
}

// readTailRecords returns the last keep records of one JSONL file. When
// tolerateTrailing is set (newest file only), an unparseable final line
// is treated as a concurrent partial write and skipped.
func readTailRecords(path, name string, keep int, tolerateTrailing bool) ([]*Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("audit: tail open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var recs []*Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	line := 0
	var pendingErr error
	for sc.Scan() {
		line++
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		// A malformed line only passes when it is the final line of the
		// newest file; buffer the error until we know whether more lines
		// follow.
		if pendingErr != nil {
			return nil, pendingErr
		}
		var r Record
		if jerr := json.Unmarshal(raw, &r); jerr != nil {
			err := fmt.Errorf("audit: tail %s line %d: invalid JSON: %w", name, line, jerr)
			if !tolerateTrailing {
				return nil, err
			}
			pendingErr = err
			continue
		}
		recs = append(recs, &r)
		if len(recs) > keep {
			recs = recs[1:]
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("audit: tail scan %s: %w", path, err)
	}
	return recs, nil
}
