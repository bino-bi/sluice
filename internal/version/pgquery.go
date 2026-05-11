// SPDX-License-Identifier: AGPL-3.0-or-later

package version

import "sync/atomic"

// parserVersion is the currently-reported parser backend version. It is
// populated at composition time via SetPgQueryVersion so leaf packages
// (audit, telemetry) do not need to import the parser backend directly
// and avoid the cgo dependency unless the binary actually uses it.
var parserVersion atomic.Pointer[string]

// SetPgQueryVersion registers the parser backend identifier discovered at
// startup. Safe to call multiple times; only the most recent value wins.
func SetPgQueryVersion(v string) {
	if v == "" {
		return
	}
	parserVersion.Store(&v)
}

// PgQueryVersion returns the libpg_query / parser backend version used at
// build time. When SetPgQueryVersion has not been called — e.g. during a
// test that doesn't spin up the full composition — a stable sentinel is
// returned so callers never see an empty string.
func PgQueryVersion() string {
	if p := parserVersion.Load(); p != nil {
		return *p
	}
	return "unknown"
}
