// SPDX-License-Identifier: AGPL-3.0-or-later

// Package schema caches the introspected column list of every table in
// every attached data source. The rewriter reads it for the column list of
// each masked table's wrapper, and to expand SELECT * and t.* on a query
// with a post-query mask. Nothing checks a row filter column against it.
//
// Semantics:
//
//   - Load on miss: there is no boot refresh and no timer. A miss for any
//     table loads the whole catalog via Loader.Load; every table in that
//     catalog is cached at once. Concurrent misses deduplicate through a
//     per-catalog lock.
//   - Stale-on-failure: a failed load marks entries Stale but leaves the
//     last good data in place; callers keep serving with a stale_entries
//     metric incremented.
//   - Invalidate: a policy reload clears every entry.
package schema
