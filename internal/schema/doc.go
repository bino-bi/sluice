// SPDX-License-Identifier: AGPL-3.0-or-later

// Package schema caches the introspected column list of every table in
// every attached data source. The rewriter calls it to expand SELECT * and
// t.*, to validate that row-filter columns exist at policy-load time, and
// to resolve masked column references without re-querying DuckDB per
// request.
//
// Semantics:
//
//   - Pull-on-boot: cmd/sluice calls Cache.Refresh after attaching every
//     data source so the first user query does not pay the introspection
//     cost.
//   - Per-catalog introspection: a miss for any table triggers loading
//     the whole catalog via Loader.Load; every table in that catalog is
//     cached at once. Concurrent misses deduplicate through a per-catalog
//     lock.
//   - Stale-on-failure: refresh failures mark entries Stale but leave the
//     last good data in place; callers keep serving with a stale_entries
//     metric incremented.
//   - Invalidate: detach / reattach of a data source clears the catalog's
//     entries.
package schema
