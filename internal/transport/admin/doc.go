// SPDX-License-Identifier: AGPL-3.0-or-later

// Package admin exposes an operator-facing HTTP API on a separate port
// from the data plane. The MVP implementation is read-only: it lists
// policies, data-source statuses, the audit tail, and proxies Explain
// requests through queryservice. Authentication is a static token
// compared in constant time; TLS + mTLS land in v1.
package admin
