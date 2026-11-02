// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package build

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive lock on path and returns the unlock func.
// The lock also holds between processes (a CLI build and the server).
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, reportMode)
	if err != nil {
		return nil, err
	}
	h := windows.Handle(f.Fd())
	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, ol); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = windows.UnlockFileEx(h, 0, 1, 0, ol)
		_ = f.Close()
	}, nil
}
