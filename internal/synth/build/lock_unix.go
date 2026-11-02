// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package build

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive lock on path and returns the unlock func.
// The lock also holds between processes (a CLI build and the server).
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, reportMode)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
