// SPDX-License-Identifier: AGPL-3.0-or-later

// Package tlsutil builds *tls.Config values for the REST and admin
// listeners from configured file paths. It exists so both transports
// share one implementation without importing each other.
package tlsutil
