// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"os"
)

// main runs the cobra root. Exit codes:
//
//	0 — success
//	1 — user/input error (bad flags, missing config, I/O)
//	3 — policy validation failure (CI gate signal)
//
// Other codes are reserved for future subcommands (2, 4, 64–78).
func main() {
	if err := newRootCmd().Execute(); err != nil {
		var exit *exitError
		if asExitError(err, &exit) {
			switch {
			case exit.Msg != "":
				_, _ = fmt.Fprintln(os.Stderr, exit.Msg)
			case exit.Err != nil:
				_, _ = fmt.Fprintln(os.Stderr, exit.Err)
			}
			os.Exit(exit.Code)
		}
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// exitError lets subcommands signal a specific process exit code through the
// ordinary (error) return channel cobra already plumbs.
type exitError struct {
	Code int
	Msg  string
	Err  error
}

func (e *exitError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Msg
}

func (e *exitError) Unwrap() error { return e.Err }

// asExitError is a thin shim around errors.As so main() can switch on the
// exitError sentinel without importing errors in two places.
func asExitError(err error, target **exitError) bool {
	return errors.As(err, target)
}
