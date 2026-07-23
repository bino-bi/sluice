// SPDX-License-Identifier: AGPL-3.0-or-later

// Package approval implements the human-approval broker: it holds
// queries that policy marked as requiring approval, fires a webhook
// carrying accept/reject capability URLs, and issues single-use grants
// that let an approved query re-run within a TTL.
//
// State lives in per-process memory; an optional Store (SQLite) persists
// it write-through so pending requests, unconsumed grants, and their
// capability links survive a restart. Without a store, a restart loses
// pending requests and unclaimed grants — callers simply re-submit,
// which mints a fresh approval request. Multi-replica deployments get
// independent brokers; that is out of scope (documented).
package approval
