// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package audit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

var errLocked = errors.New("audit: directory locked")

// tryLock takes an exclusive flock on f without waiting. The lock ends
// when f is closed or the process exits.
func tryLock(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errLocked
	}
	return err
}

// checkDirOwner refuses a directory of another user, or one that group or
// others can write.
func checkDirOwner(dir string, info fs.FileInfo) error {
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("audit: group or others can write the audit directory %s (mode %v), run chmod go-w on it", dir, info.Mode().Perm())
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if uid := os.Geteuid(); int(st.Uid) != uid {
		return fmt.Errorf("audit: the audit directory %s belongs to uid %d, sluice runs as uid %d", dir, st.Uid, uid)
	}
	return nil
}
