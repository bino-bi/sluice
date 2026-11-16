// SPDX-License-Identifier: AGPL-3.0-or-later

package layout

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const (
	idOld = "20261102T101500Z-a1b2c3"
	idCur = "20261102T101600Z-a1b2c3"
	idNew = "20261102T101700Z-a1b2c3"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func snapshotNames(t *testing.T, l Layout) []string {
	t.Helper()
	entries, err := os.ReadDir(l.SnapshotsDir())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// A killed build leaves its temp file; the next publish removes the temp
// files of older ids and keeps those of newer ones.
func TestPublishRemovesOlderTemps(t *testing.T) {
	l := Layout{Dir: t.TempDir(), Name: "crmsynth"}
	if err := l.Mkdirs(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{idOld, idNew} {
		touch(t, l.TempSnapshot(id))
		touch(t, l.TempSnapshot(id)+".wal")
	}
	touch(t, filepath.Join(l.SnapshotsDir(), ".notes.duckdb.tmp"))
	touch(t, l.Snapshot(idCur))
	if err := l.Publish(idCur); err != nil {
		t.Fatal(err)
	}
	want := []string{"." + idNew + ".duckdb.tmp", "." + idNew + ".duckdb.tmp.wal", ".notes.duckdb.tmp", idCur + ".duckdb"}
	if got := snapshotNames(t, l); !slices.Equal(got, want) {
		t.Errorf("snapshots = %v, want %v", got, want)
	}

	l.RemoveTemps(func(string) bool { return true })
	want = []string{".notes.duckdb.tmp", idCur + ".duckdb"}
	if got := snapshotNames(t, l); !slices.Equal(got, want) {
		t.Errorf("after RemoveTemps: %v, want %v", got, want)
	}
}
