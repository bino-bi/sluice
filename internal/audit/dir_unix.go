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

// checkDirMode refuses a directory that every user can write. A group
// writable directory passes: a volume with an fsGroup is one.
func checkDirMode(dir string, info fs.FileInfo) error {
	if info.Mode().Perm()&0o002 != 0 {
		return fmt.Errorf("audit: every user can write the audit directory %s (mode %v), run chmod o-w on it", dir, info.Mode().Perm())
	}
	return nil
}
