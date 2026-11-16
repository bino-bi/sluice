// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package layout

import (
	"os"
	"syscall"
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
	how := syscall.LOCK_EX
	if !block {
		how |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
