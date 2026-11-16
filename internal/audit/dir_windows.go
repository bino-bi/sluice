// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package audit

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

var errLocked = errors.New("audit: directory locked")

// tryLock takes an exclusive lock on f without waiting. The lock ends
// when f is closed or the process exits.
func tryLock(f *os.File) error {
	ol := new(windows.Overlapped)
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errLocked
	}
	return err
}

// checkDirOwner has no owner check on Windows: ACLs are out of scope.
func checkDirOwner(string, fs.FileInfo) error { return nil }
