// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ErrInvalidBuildID is returned for a build id that does not have the
// form 20261102T101500Z-a1b2c3. The error never holds the value.
var ErrInvalidBuildID = errors.New("invalid build id")

// ErrNewerCurrent is returned when another build published a newer id
// while this one ran.
var ErrNewerCurrent = errors.New("a newer build is current")

var buildIDPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}$`)

// ValidBuildID reports whether id has the build id form. Every id is
// checked before it is used in a path.
func ValidBuildID(id string) bool { return buildIDPattern.MatchString(id) }

// newBuildID returns the UTC time plus 6 random hex chars, so ids sort by
// time.
func newBuildID(now time.Time) (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:]), nil
}

const (
	dirMode    = 0o750
	reportMode = 0o600
)

// Layout is the folder of one synthetic data source:
//
//	<dir>/<name>/current                       one line: the build id
//	<dir>/<name>/snapshots/<build-id>.duckdb   the data
//	<dir>/<name>/reports/<build-id>.json       the report
//
// The reports are outside the snapshot folder, which is the only folder
// the query pool can open.
type Layout struct {
	Dir, Name string
}

// Root returns <dir>/<name>.
func (l Layout) Root() string { return filepath.Join(l.Dir, l.Name) }

// SnapshotsDir returns the snapshot folder.
func (l Layout) SnapshotsDir() string { return filepath.Join(l.Root(), "snapshots") }

// ReportsDir returns the report folder.
func (l Layout) ReportsDir() string { return filepath.Join(l.Root(), "reports") }

// Snapshot returns the snapshot path of a build. The id must be valid.
func (l Layout) Snapshot(id string) string {
	return filepath.Join(l.SnapshotsDir(), id+".duckdb")
}

// Report returns the report path of a build. The id must be valid.
func (l Layout) Report(id string) string { return filepath.Join(l.ReportsDir(), id+".json") }

func (l Layout) tempSnapshot(id string) string {
	return filepath.Join(l.SnapshotsDir(), "."+id+".duckdb.tmp")
}

func (l Layout) current() string { return filepath.Join(l.Root(), "current") }

// ReadCurrent returns the id in the current file, or "" when there is no
// file. An id that is not valid fails with ErrInvalidBuildID.
func (l Layout) ReadCurrent() (string, error) {
	b, err := os.ReadFile(l.current())
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(b))
	if !ValidBuildID(id) {
		return "", fmt.Errorf("%s: %w", l.current(), ErrInvalidBuildID)
	}
	return id, nil
}

// ReadReport reads the report of a build.
func (l Layout) ReadReport(id string) (*Report, error) {
	if !ValidBuildID(id) {
		return nil, ErrInvalidBuildID
	}
	b, err := os.ReadFile(l.Report(id))
	if err != nil {
		return nil, err
	}
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("report %s: %w", id, err)
	}
	return &r, nil
}

func (l Layout) mkdirs() error {
	for _, d := range []string{l.SnapshotsDir(), l.ReportsDir()} {
		if err := os.MkdirAll(d, dirMode); err != nil {
			return err
		}
	}
	return nil
}

// writeReport writes the report with mode 0600 through a temp file.
func (l Layout) writeReport(r *Report) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(l.Report(r.BuildID), append(b, '\n'), reportMode)
}

// writeFileAtomic writes data to a temp file in the same folder, syncs it
// and renames it to path.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

func syncFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	err = f.Sync()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// syncDir makes a rename in dir durable. Windows cannot sync a folder,
// so the error is ignored there.
func syncDir(dir string) {
	_ = syncFile(dir)
}

// publish points current at id under the publish lock (plan 1.5 step 4)
// and runs retention. It fails with ErrNewerCurrent when current already
// holds a newer id.
func (l Layout) publish(id string) error {
	unlock, err := lockFile(filepath.Join(l.Root(), "publish.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	prev, err := l.ReadCurrent()
	if err != nil {
		return err
	}
	if prev > id {
		return ErrNewerCurrent
	}
	if err := writeFileAtomic(l.current(), []byte(id+"\n"), reportMode); err != nil {
		return err
	}
	syncDir(l.Root())
	l.retain(id, prev)
	return nil
}

// retain keeps the current and the previous build and removes finished
// snapshots and reports whose id sorts before the previous id. current
// only moves forward, so such an id is finished or failed. Temp files
// are never touched. Errors are ignored: a file left behind is removed
// by the next build.
func (l Layout) retain(cur, prev string) {
	if prev == "" {
		return
	}
	for _, d := range []struct{ dir, ext string }{
		{l.SnapshotsDir(), ".duckdb"},
		{l.ReportsDir(), ".json"},
	} {
		entries, err := os.ReadDir(d.dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			id, ok := strings.CutSuffix(e.Name(), d.ext)
			if !ok || !ValidBuildID(id) || id == cur || id >= prev || !e.Type().IsRegular() {
				continue
			}
			_ = os.Remove(filepath.Join(d.dir, e.Name()))
		}
	}
}
