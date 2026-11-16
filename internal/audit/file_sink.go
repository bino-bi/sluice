// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// auditNameRe matches the trailing YYYY-MM-DD[-seq] of an audit filename,
// independent of the configured prefix.
var auditNameRe = regexp.MustCompile(`(\d{4}-\d{2}-\d{2})(?:-(\d+))?\.jsonl$`)

// auditNameKey returns the (date, seq) sort key for an audit filename.
func auditNameKey(name string) (string, int, bool) {
	m := auditNameRe.FindStringSubmatch(name)
	if m == nil {
		return "", 0, false
	}
	seq := 0
	if m[2] != "" {
		seq, _ = strconv.Atoi(m[2])
	}
	return m[1], seq, true
}

// sortAuditNames orders audit filenames chronologically by (date, seq)
// rather than lexicographically. A size-rotated `...-D-1.jsonl` must sort
// AFTER the seq-0 `...-D.jsonl`, which plain byte order gets wrong because
// '-' (0x2D) sorts before '.' (0x2E). Unparseable names fall back to
// lexicographic order so the sort stays total.
func sortAuditNames(names []string) {
	sort.Slice(names, func(i, j int) bool {
		di, si, oki := auditNameKey(names[i])
		dj, sj, okj := auditNameKey(names[j])
		if !oki || !okj {
			return names[i] < names[j]
		}
		if di != dj {
			return di < dj
		}
		return si < sj
	})
}

// FileOptions configures a FileSink.
type FileOptions struct {
	// Dir is the audit directory. Created with permission 0700 if missing.
	Dir string

	// FilePrefix overrides the default "audit-" prefix used in filenames
	// (`audit-YYYY-MM-DD.jsonl` / `audit-YYYY-MM-DD-N.jsonl`).
	FilePrefix string

	// RotateDaily enables midnight-UTC rotation. Default true.
	RotateDaily bool

	// RotateSizeMB triggers rotation once the current file grows past this
	// many mebibytes. 0 disables size rotation.
	RotateSizeMB int

	// FileMode is the permission set on created audit files. Default 0640.
	FileMode os.FileMode

	// DirMode is the permission of a new audit directory. Default 0700.
	DirMode os.FileMode

	// Clock overrides time.Now for deterministic tests.
	Clock func() time.Time
}

// FileSink is the default audit sink: append-only JSONL with daily + size
// rotation and periodic fsync. Each record is one write call, so a record
// is never split over two writes and a failed write loses no other record.
// The caller (Dispatcher) is responsible for invoking Flush on a cadence;
// FileSink itself does not spawn goroutines.
type FileSink struct {
	mu sync.Mutex

	dir      string
	prefix   string
	rotateMB int64
	daily    bool
	fileMode os.FileMode
	clock    func() time.Time
	closed   bool

	// lock holds the exclusive lock of the directory until Close.
	lock    *os.File
	file    *os.File
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

	// lastAtOpen is the newest record found when the sink opened.
	lastAtOpen *Record
}

const (
	defaultMode    = os.FileMode(0o640)
	defaultDirMode = os.FileMode(0o700)
	defaultPrefix  = "audit-"
	lockFileName   = ".sluice.lock"
)

// NewFileSink opens (or creates) the latest audit file in opts.Dir and
// primes the hash chain from it. The returned sink is ready to Record. It
// holds an exclusive lock on the directory until Close: two processes that
// append to one chain would fork it.
func NewFileSink(opts FileOptions) (*FileSink, error) {
	if opts.Dir == "" {
		return nil, fmt.Errorf("audit: FileOptions.Dir required")
	}
	if opts.FilePrefix == "" {
		opts.FilePrefix = defaultPrefix
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

	dir := filepath.Clean(opts.Dir)
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, opts.DirMode); err != nil {
			return nil, fmt.Errorf("audit: mkdir %s: %w", dir, err)
		}
	}
	if err := checkDir(dir); err != nil {
		return nil, err
	}
	lock, err := lockDir(dir)
	if err != nil {
		return nil, err
	}

	s := &FileSink{
		dir:      dir,
		prefix:   opts.FilePrefix,
		rotateMB: int64(opts.RotateSizeMB) * 1024 * 1024,
		// Daily by default. RotateDaily false with RotateSizeMB > 0 keeps
		// daily rotation off.
		daily:    opts.RotateDaily || opts.RotateSizeMB == 0,
		fileMode: opts.FileMode,
		clock:    opts.Clock,
		lock:     lock,
	}
	if err := s.openLatest(); err != nil {
		_ = lock.Close()
		return nil, err
	}
	return s, nil
}

// checkDir refuses an audit directory that another user could change or
// redirect.
func checkDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("audit: the audit directory %s is a symlink, use the real path", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("audit: the audit directory %s is not a directory", dir)
	}
	return checkDirOwner(dir, info)
}

// lockDir takes the exclusive lock of dir without waiting.
func lockDir(dir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, lockFileName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("audit: open the lock file in %s: %w", dir, err)
	}
	if err := tryLock(f); err != nil {
		_ = f.Close()
		if errors.Is(err, errLocked) {
			return nil, fmt.Errorf("audit: the audit directory %s is in use by another sluice process: give each process its own audit.file.path, or run `sluice serve` with mcp.enabled instead of a separate `sluice mcp`", dir)
		}
		return nil, fmt.Errorf("audit: lock %s: %w", dir, err)
	}
	return f, nil
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
// by the caller; FileSink never mutates them. After a failed write it
// reopens the file and tries once more.
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
	if s.file == nil {
		if err := s.reopen(); err != nil {
			return err
		}
	}
	if err := s.rotateIfNeeded(int64(len(line))); err != nil {
		return err
	}
	if err := s.writeLine(line); err != nil {
		// The reopen cuts off the part of the line that reached the file.
		if rerr := s.reopen(); rerr != nil {
			return fmt.Errorf("audit: write: %w, reopen: %w", err, rerr)
		}
		if err := s.writeLine(line); err != nil {
			s.dropFile()
			return fmt.Errorf("audit: write: %w", err)
		}
	}
	s.lastHash = r.Hash
	return nil
}

// writeLine writes line in one call. Must be called with s.mu held.
func (s *FileSink) writeLine(line []byte) error {
	n, err := s.file.Write(line)
	s.written += int64(n)
	return err
}

// reopen closes the active file without a sync and opens the file of the
// current day and seq again. Must be called with s.mu held.
func (s *FileSink) reopen() error {
	s.dropFile()
	f, size, err := s.openForDay(s.day, s.seq)
	if err != nil {
		return err
	}
	s.file, s.written = f, size
	return nil
}

// dropFile closes the active file and forgets it. Must be called with s.mu
// held.
func (s *FileSink) dropFile() {
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
}

// Flush persists the written records to disk.
func (s *FileSink) Flush(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.file == nil {
		return nil
	}
	if err := s.file.Sync(); err != nil {
		return fmt.Errorf("audit: fsync: %w", err)
	}
	return nil
}

// Close syncs and closes the active file and releases the directory lock.
// Idempotent.
func (s *FileSink) Close(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var err error
	if s.file != nil {
		err = closeFile(s.file)
		s.file = nil
	}
	_ = s.lock.Close()
	return err
}

// closeFile syncs and closes f.
func closeFile(f *os.File) error {
	var firstErr error
	if err := f.Sync(); err != nil {
		firstErr = fmt.Errorf("audit: fsync: %w", err)
	}
	if err := f.Close(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("audit: close: %w", err)
	}
	return firstErr
}

// rotateIfNeeded opens a new file when either the UTC day changed or the
// upcoming write would exceed the size cap. The state changes only after
// the new file is open, so a failed open is tried again on the next
// record. Must be called with s.mu held.
func (s *FileSink) rotateIfNeeded(upcoming int64) error {
	now := s.clock().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	dailyCrossed := s.daily && !s.day.Equal(today)
	sizeExceeded := s.rotateMB > 0 && s.written+upcoming > s.rotateMB

	if !dailyCrossed && !sizeExceeded {
		return nil
	}
	start := time.Now()
	day, seq := s.day, s.seq+1
	if dailyCrossed {
		day, seq = today, 0
	}
	f, size, err := s.openForDay(day, seq)
	metricsShared().RotateDuration.WithLabelValues(s.Name()).Observe(time.Since(start).Seconds())
	if err != nil {
		return err
	}
	if s.file != nil {
		// The records of the old file are written, only the sync failed.
		if err := closeFile(s.file); err != nil {
			metricsShared().WriteErrors.WithLabelValues(s.Name()).Inc()
		}
	}
	s.file, s.written, s.day, s.seq = f, size, day, seq
	return nil
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
	sortAuditNames(picks)

	now := s.clock().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	day, seq := today, 0
	if len(picks) > 0 {
		latest := picks[len(picks)-1]
		d, n, err := parseName(s.prefix, latest)
		if err != nil {
			return fmt.Errorf("audit: parse existing file %q: %w", latest, err)
		}
		if err := s.primeLastHash(picks); err != nil {
			return err
		}
		// A newest file of an earlier UTC day gets a new file for today.
		// Otherwise append to it, or to the next seq when it is over the
		// size cap.
		if !s.daily || d.Equal(today) {
			day, seq = d, n
			info, err := os.Stat(filepath.Join(s.dir, latest))
			if err != nil {
				return fmt.Errorf("audit: stat %s: %w", latest, err)
			}
			if s.rotateMB > 0 && info.Size() >= s.rotateMB {
				seq++
			}
		}
	}
	f, size, err := s.openForDay(day, seq)
	if err != nil {
		return err
	}
	s.file, s.written, s.day, s.seq = f, size, day, seq
	return nil
}

// openForDay opens audit-YYYY-MM-DD[-seq].jsonl for append, creates it
// when missing, and returns it with its size. It moves a torn last line
// aside first. It does not change the state of s.
func (s *FileSink) openForDay(day time.Time, seq int) (*os.File, int64, error) {
	path := filepath.Join(s.dir, formatName(s.prefix, day, seq))
	if err := s.repairTornTail(path); err != nil {
		return nil, 0, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, s.fileMode)
	if err != nil {
		return nil, 0, fmt.Errorf("audit: open %s: %w", path, err)
	}
	// Tighten mode on platforms where O_CREATE ignored it.
	if err := os.Chmod(path, s.fileMode); err != nil {
		_ = f.Close()
		return nil, 0, fmt.Errorf("audit: chmod %s: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, fmt.Errorf("audit: stat %s: %w", path, err)
	}
	return f, info.Size(), nil
}

// repairTornTail moves a last line without newline, left by a crash or a
// failed write, to <path>.torn-<time> and cuts it from path. That line was
// never a complete record, so the chain stays intact. A missing file is
// fine.
func (s *FileSink) repairTornTail(path string) error {
	torn, err := endsTorn(path)
	if err != nil || !torn {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("audit: read %s: %w", path, err)
	}
	keep := bytes.LastIndexByte(data, '\n') + 1
	side := path + ".torn-" + s.clock().UTC().Format("20060102T150405.000000000Z")
	if err := os.WriteFile(side, data[keep:], s.fileMode); err != nil {
		return fmt.Errorf("audit: save the torn last line of %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("audit: open %s: %w", path, err)
	}
	if err := f.Truncate(int64(keep)); err != nil {
		_ = f.Close()
		return fmt.Errorf("audit: cut the torn last line of %s: %w", path, err)
	}
	if err := closeFile(f); err != nil {
		return err
	}
	slog.Default().Error("audit: the audit file ended in a torn line, moved it aside",
		slog.String("file", path), slog.String("moved_to", side), slog.Int("bytes", len(data)-keep))
	return nil
}

// endsTorn reports whether path is not empty and its last byte is not a
// newline. It reads one byte, files can be large. A missing file is fine.
func endsTorn(path string) (bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("audit: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return false, fmt.Errorf("audit: stat %s: %w", path, err)
	}
	if info.Size() == 0 {
		return false, nil
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return false, fmt.Errorf("audit: read %s: %w", path, err)
	}
	return last[0] != '\n', nil
}

// primeLastHash takes the chain tip from the newest record in names, which
// are in chain order. Without any record it leaves lastHash at the zero
// value (caller handles genesis bootstrap).
func (s *FileSink) primeLastHash(names []string) error {
	// The newest file can be empty: a start that failed after it created
	// the file, or a rotation without a record yet. The chain tip is then
	// in a file before it.
	for i := len(names) - 1; i >= 0; i-- {
		path := filepath.Join(s.dir, names[i])
		if err := s.repairTornTail(path); err != nil {
			return err
		}
		last, err := readLastRecord(path)
		if err != nil {
			return err
		}
		if last != nil {
			s.lastHash = last.Hash
			s.lastAtOpen = last
			return nil
		}
	}
	return nil
}

// LastRecord returns the newest record that was in the directory when the
// sink opened it, or nil for an empty directory.
func (s *FileSink) LastRecord() *Record { return s.lastAtOpen }

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
// Record. An empty file returns (nil, nil). A last line that is not a
// complete record is an error: callers repair a torn tail first.
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
