// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// FileOptions configures a FileSink.
type FileOptions struct {
	// Dir is the audit directory. Created with permission 0750 if missing.
	Dir string

	// FilePrefix overrides the default "audit-" prefix used in filenames
	// (`audit-YYYY-MM-DD.jsonl` / `audit-YYYY-MM-DD-N.jsonl`).
	FilePrefix string

	// RotateDaily enables midnight-UTC rotation. Default true.
	RotateDaily bool

	// RotateSizeMB triggers rotation once the current file grows past this
	// many mebibytes. 0 disables size rotation.
	RotateSizeMB int

	// BufferBytes sizes the bufio.Writer. Default 256 KiB.
	BufferBytes int

	// FileMode is the permission set on created audit files. Default 0640.
	FileMode os.FileMode

	// DirMode is the permission set on the audit directory. Default 0750.
	DirMode os.FileMode

	// Clock overrides time.Now for deterministic tests.
	Clock func() time.Time
}

// FileSink is the default audit sink: append-only JSONL with daily + size
// rotation and periodic fsync. The caller (Dispatcher) is responsible for
// invoking Flush on a cadence; FileSink itself does not spawn goroutines.
type FileSink struct {
	mu sync.Mutex

	dir      string
	prefix   string
	rotateMB int64
	daily    bool
	bufSize  int
	fileMode os.FileMode
	clock    func() time.Time
	closed   bool

	file    *os.File
	writer  *bufio.Writer
	written int64
	// day is the UTC date of the currently-open file (midnight). Used to
	// detect daily rotation.
	day time.Time
	// seq is the increment used when a single UTC day sees multiple
	// size-triggered rotations (audit-YYYY-MM-DD.jsonl, -1.jsonl, …).
	seq int

	// lastHash is the hash of the most recently written record in this
	// sink, used by the dispatcher as the PriorHash for the next record.
	lastHash string
}

const (
	defaultBuffer  = 256 * 1024
	defaultMode    = os.FileMode(0o640)
	defaultDirMode = os.FileMode(0o750)
	defaultPrefix  = "audit-"
)

// NewFileSink opens (or creates) the latest audit file in opts.Dir and
// primes the hash chain from it. The returned sink is ready to Record.
func NewFileSink(opts FileOptions) (*FileSink, error) {
	if opts.Dir == "" {
		return nil, fmt.Errorf("audit: FileOptions.Dir required")
	}
	if opts.FilePrefix == "" {
		opts.FilePrefix = defaultPrefix
	}
	if opts.BufferBytes == 0 {
		opts.BufferBytes = defaultBuffer
	}
	if opts.FileMode == 0 {
		opts.FileMode = defaultMode
	}
	if opts.DirMode == 0 {
		opts.DirMode = defaultDirMode
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}

	if err := os.MkdirAll(opts.Dir, opts.DirMode); err != nil {
		return nil, fmt.Errorf("audit: mkdir %s: %w", opts.Dir, err)
	}

	s := &FileSink{
		dir:      opts.Dir,
		prefix:   opts.FilePrefix,
		rotateMB: int64(opts.RotateSizeMB) * 1024 * 1024,
		daily:    opts.RotateDaily || !opts.RotateDaily && opts.RotateSizeMB == 0, // default daily
		bufSize:  opts.BufferBytes,
		fileMode: opts.FileMode,
		clock:    opts.Clock,
	}
	// Honour the caller's explicit choice: if RotateDaily is false but
	// RotateSizeMB > 0, we keep daily=false.
	s.daily = opts.RotateDaily || opts.RotateSizeMB == 0

	if err := s.openLatest(); err != nil {
		return nil, err
	}
	return s, nil
}

// Name satisfies Sink.
func (s *FileSink) Name() string { return "file" }

// LastHash returns the hash of the last successfully appended record. The
// dispatcher reads this during startup so a new process continues the
// chain from where the previous one stopped.
func (s *FileSink) LastHash() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastHash
}

// Record appends r to the active file. r.PriorHash and r.Hash must be set
// by the caller; FileSink never mutates them.
func (s *FileSink) Record(_ context.Context, r *Record) error {
	line, err := MarshalLine(r)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}

	if err := s.rotateIfNeeded(int64(len(line))); err != nil {
		return err
	}
	n, err := s.writer.Write(line)
	if err != nil {
		return fmt.Errorf("audit: write: %w", err)
	}
	s.written += int64(n)
	s.lastHash = r.Hash
	return nil
}

// Flush persists buffered writes to disk.
func (s *FileSink) Flush(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.writer == nil {
		return nil
	}
	if err := s.writer.Flush(); err != nil {
		return fmt.Errorf("audit: flush: %w", err)
	}
	if err := s.file.Sync(); err != nil {
		return fmt.Errorf("audit: fsync: %w", err)
	}
	return nil
}

// Close flushes and closes the active file. Idempotent.
func (s *FileSink) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.writer == nil {
		return nil
	}
	var firstErr error
	if err := s.writer.Flush(); err != nil {
		firstErr = fmt.Errorf("audit: flush on close: %w", err)
	}
	if err := s.file.Sync(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("audit: fsync on close: %w", err)
	}
	if err := s.file.Close(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("audit: close: %w", err)
	}
	_ = ctx
	return firstErr
}

// rotateIfNeeded flushes + closes the current file and opens a new one
// when either the UTC day changed or the upcoming write would exceed the
// size cap. Must be called with s.mu held.
func (s *FileSink) rotateIfNeeded(upcoming int64) error {
	now := s.clock().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	dailyCrossed := s.daily && !s.day.Equal(today)
	sizeExceeded := s.rotateMB > 0 && s.written+upcoming > s.rotateMB

	if !dailyCrossed && !sizeExceeded {
		return nil
	}
	if err := s.closeActive(); err != nil {
		return err
	}
	if dailyCrossed {
		s.day = today
		s.seq = 0
	} else {
		s.seq++
	}
	return s.openForDay(s.day, s.seq)
}

// openLatest chooses the newest file in the directory (or opens today's
// fresh file when the dir is empty) and primes lastHash from the tail.
func (s *FileSink) openLatest() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("audit: read dir: %w", err)
	}
	var picks []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, s.prefix) || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		picks = append(picks, name)
	}
	sort.Strings(picks)

	now := s.clock().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	if len(picks) == 0 {
		s.day = today
		s.seq = 0
		return s.openForDay(today, 0)
	}

	latest := picks[len(picks)-1]
	day, seq, err := parseName(s.prefix, latest)
	if err != nil {
		return fmt.Errorf("audit: parse existing file %q: %w", latest, err)
	}
	// If the newest file is from an earlier UTC day, start a new file for
	// today; otherwise append to the same one.
	if s.daily && !day.Equal(today) {
		// Prime lastHash from the previous file, then open today's 0-seq.
		if err := s.primeLastHash(filepath.Join(s.dir, latest)); err != nil {
			return err
		}
		s.day = today
		s.seq = 0
		return s.openForDay(today, 0)
	}

	// Reopen the existing file. If it exceeds the size cap, bump seq.
	path := filepath.Join(s.dir, latest)
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("audit: stat %s: %w", path, err)
	}
	if err := s.primeLastHash(path); err != nil {
		return err
	}
	if s.rotateMB > 0 && info.Size() >= s.rotateMB {
		s.day = day
		s.seq = seq + 1
		return s.openForDay(s.day, s.seq)
	}
	s.day = day
	s.seq = seq
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, s.fileMode)
	if err != nil {
		return fmt.Errorf("audit: reopen %s: %w", path, err)
	}
	s.file = f
	s.writer = bufio.NewWriterSize(f, s.bufSize)
	s.written = info.Size()
	return nil
}

// openForDay opens audit-YYYY-MM-DD[-seq].jsonl fresh (O_CREATE|O_APPEND).
// Must be called with s.mu held.
func (s *FileSink) openForDay(day time.Time, seq int) error {
	name := formatName(s.prefix, day, seq)
	path := filepath.Join(s.dir, name)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, s.fileMode)
	if err != nil {
		return fmt.Errorf("audit: open %s: %w", path, err)
	}
	// Tighten mode on platforms where O_CREATE ignored it.
	if err := os.Chmod(path, s.fileMode); err != nil {
		_ = f.Close()
		return fmt.Errorf("audit: chmod %s: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("audit: stat %s: %w", path, err)
	}
	s.file = f
	s.writer = bufio.NewWriterSize(f, s.bufSize)
	s.written = info.Size()
	return nil
}

// closeActive flushes + closes the current file. Must be called with s.mu held.
func (s *FileSink) closeActive() error {
	if s.writer == nil {
		return nil
	}
	var firstErr error
	if err := s.writer.Flush(); err != nil {
		firstErr = fmt.Errorf("audit: flush: %w", err)
	}
	if err := s.file.Sync(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("audit: fsync: %w", err)
	}
	if err := s.file.Close(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("audit: close: %w", err)
	}
	s.writer = nil
	s.file = nil
	s.written = 0
	return firstErr
}

// primeLastHash reads the tail of path and extracts the final record's
// hash so the dispatcher chains the next record correctly. An empty file
// leaves lastHash at the zero value (caller handles genesis bootstrap).
func (s *FileSink) primeLastHash(path string) error {
	last, err := readLastRecord(path)
	if err != nil {
		return err
	}
	if last != nil {
		s.lastHash = last.Hash
	}
	return nil
}

// parseName extracts (day, seq) from an audit filename. Seq is 0 when the
// name omits the trailing `-N`.
func parseName(prefix, name string) (time.Time, int, error) {
	stem := strings.TrimPrefix(name, prefix)
	stem = strings.TrimSuffix(stem, ".jsonl")
	// stem is either YYYY-MM-DD or YYYY-MM-DD-N.
	if len(stem) < 10 {
		return time.Time{}, 0, fmt.Errorf("bad name %q", name)
	}
	dayStr := stem[:10]
	day, err := time.Parse("2006-01-02", dayStr)
	if err != nil {
		return time.Time{}, 0, err
	}
	if len(stem) == 10 {
		return day, 0, nil
	}
	if stem[10] != '-' {
		return time.Time{}, 0, fmt.Errorf("bad name %q", name)
	}
	var n int
	if _, err := fmt.Sscanf(stem[11:], "%d", &n); err != nil {
		return time.Time{}, 0, err
	}
	return day, n, nil
}

// formatName is parseName's inverse.
func formatName(prefix string, day time.Time, seq int) string {
	base := day.UTC().Format("2006-01-02")
	if seq == 0 {
		return prefix + base + ".jsonl"
	}
	return fmt.Sprintf("%s%s-%d.jsonl", prefix, base, seq)
}

// readLastRecord reads the last JSON line of path and decodes it into a
// Record. An empty file returns (nil, nil). Partial trailing lines (no
// newline) are treated as empty; the dispatcher will chain from the last
// complete record.
func readLastRecord(path string) (*Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("audit: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	// Trim trailing whitespace.
	data = bytes.TrimRight(data, "\n\r\t ")
	if len(data) == 0 {
		return nil, nil
	}
	// Find the last newline — the last line starts after it.
	idx := bytes.LastIndexByte(data, '\n')
	line := data
	if idx >= 0 {
		line = data[idx+1:]
	}
	var r Record
	dec := json.NewDecoder(bytes.NewReader(line))
	if err := dec.Decode(&r); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, fmt.Errorf("audit: decode last line of %s: %w", path, err)
	}
	return &r, nil
}
