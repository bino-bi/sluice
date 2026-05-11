// SPDX-License-Identifier: AGPL-3.0-or-later

package sqlitefile

// init registers the driver with pkg/datasource. Tests that need a clean
// registry state should construct a driver manually rather than going
// through Lookup.
func init() {
	Register()
}
