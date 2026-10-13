// SPDX-License-Identifier: AGPL-3.0-or-later

// Package tlsutil builds *tls.Config values for the REST, MCP and admin
// listeners from configured file paths. It exists so the transports
// share one implementation without importing each other.
package tlsutil
