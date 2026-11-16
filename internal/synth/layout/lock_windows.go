// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package layout

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive lock on path and returns the unlock func.
// The lock also holds between processes (a CLI build and the server), and
// between two opens in one process. Without block it fails at once when
// another holder has the lock.
func lockFile(path string, block bool) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, ReportMode)
	if err != nil {
		return nil, err
	}
	h := windows.Handle(f.Fd())
	ol := new(windows.Overlapped)
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if !block {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	if err := windows.LockFileEx(h, flags, 0, 1, 0, ol); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = windows.UnlockFileEx(h, 0, 1, 0, ol)
		_ = f.Close()
	}, nil
}
