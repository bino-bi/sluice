// SPDX-License-Identifier: AGPL-3.0-or-later

// Package queryservice is the single orchestrator every transport calls.
// It owns the canonical request lifecycle:
//
//	parse → identify tables → policy.Evaluate → rewriter.Rewrite →
//	executor.Execute → audit.Enqueue
//
// Transports (REST, MCP, Admin) translate their wire formats into a
// QueryRequest, pass it to Service.Execute, and render the returned
// QueryResult or APIError. The package enforces that every terminal path
// — success, deny, reject, rewrite error, execution error — emits
// exactly one audit record.
package queryservice
