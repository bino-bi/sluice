// SPDX-License-Identifier: AGPL-3.0-or-later

package schema

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"

	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

// Key identifies a cached Entry. Catalog maps to a DataSource.Name;
// Schema and Table match the information_schema values reported by the
// driver, preserving case.
type Key struct {
	Catalog string
	Schema  string
	Table   string
}

// String renders the key as "catalog.schema.table".
func (k Key) String() string { return k.Catalog + "." + k.Schema + "." + k.Table }

// Entry is a snapshot of one table. Columns are sorted by ordinal
// position; Comment is the table-level comment when the driver reports
// one. Stale flips to true after a refresh failure.
type Entry struct {
	Key     Key
	Columns []pkgds.Column
	Comment string
	Fetched time.Time
	Stale   bool
}

// Loader fetches the catalog-wide schema of one data source. An
// implementation wraps a DataSource through a pooled connection; the
// cache never holds the connection itself.
type Loader interface {
	// Load returns the entire Schema for a single catalog. An
	// ErrUnknownCatalog return communicates "no such catalog" to the
	// cache; any other error is surfaced as ErrLoadFailed.
	Load(ctx context.Context, catalog string) (pkgds.Schema, error)
}

// Cache is the lookup interface consumed by the rewriter and admin API.
// Implementations are safe for concurrent use.
type Cache interface {
	// Get returns the Entry for key. On miss, the whole catalog is loaded
	// synchronously and every table is cached; ErrUnknownTable is
	// returned when the key is still absent after the load.
	Get(ctx context.Context, key Key) (*Entry, error)

	// All returns every cached Entry in deterministic catalog order. The
	// slice is a snapshot; mutating it does not affect the cache.
	All() []*Entry

	// Invalidate evicts a single entry so the next Get re-introspects.
	Invalidate(key Key)

	// InvalidateCatalog evicts every entry belonging to catalog. Nothing
	// calls it today.
	InvalidateCatalog(catalog string)

	// InvalidateAll drops every entry. Called on a policy reload.
	InvalidateAll()

	// Refresh loads every known catalog. On first call it also primes the
	// list of known catalogs. Errors are aggregated; callers log them but
	// always see the most recent good data.
	Refresh(ctx context.Context, catalogs []string) error

	// Load reads each catalog in catalogs that was not read since the
	// cache was last emptied. Get loads a catalog only on a miss, so a
	// caller that lists tables calls Load first. A failed load is logged
	// and tried again after LoadRetry at the earliest.
	Load(ctx context.Context, catalogs []string)
}

// Options configures a Cache. Nil-valued functions fall back to
// reasonable defaults; the only required field is Loader.
type Options struct {
	// Loader is required. Cache panics on New when nil.
	Loader Loader

	// TTL is the age at which Refresh loads a catalog again. Get does not
	// check it, and nothing calls Refresh today, so an entry stays until
	// it is invalidated. Zero means DefaultTTL.
	TTL time.Duration

	// Capacity is the maximum number of entries retained in the LRU.
	// Zero means DefaultCapacity.
	Capacity int

	// Logger receives slog output. Nil uses slog.Default.
	Logger *slog.Logger

	// Clock returns "now". Nil uses time.Now.
	Clock func() time.Time
}

// Defaults — tuned for the MVP workload.
const (
	// DefaultTTL is 10 minutes. Only Refresh reads the TTL.
	DefaultTTL = 10 * time.Minute

	// DefaultCapacity matches the performance budget in
	// concept §4.12: 10 000 tables per instance.
	DefaultCapacity = 10_000

	// LoadRetry is the time Load waits before it reads a catalog again
	// whose load failed.
	LoadRetry = 30 * time.Second
)

// lruCache is the only Cache implementation in MVP.
type lruCache struct {
	opts    Options
	entries *lru.Cache[Key, *Entry]

	// catalogSet is the set of catalogs loaded since the cache was last
	// emptied. Used by Load, and by Refresh when callers don't pass an
	// explicit list. Protected by mu.
	mu           sync.Mutex
	catalogSet   map[string]struct{}
	catalogLocks map[string]*sync.Mutex // per-catalog load dedup
	loads        map[string]uint64      // completed loads per catalog
	failed       map[string]time.Time   // last failed Load per catalog
	gen          uint64                 // counts InvalidateCatalog and InvalidateAll

	metrics *metrics
}

// New returns a fresh Cache. Panics when opts.Loader is nil — callers
// must wire a Loader.
func New(opts Options) Cache {
	if opts.Loader == nil {
		panic("schema.New: Loader is required")
	}
	if opts.TTL <= 0 {
		opts.TTL = DefaultTTL
	}
	if opts.Capacity <= 0 {
		opts.Capacity = DefaultCapacity
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	lc, err := lru.New[Key, *Entry](opts.Capacity)
	if err != nil {
		// lru.New only errors on non-positive capacity, which we enforce
		// above. Panic so a bad constant in a later refactor is caught.
		panic(fmt.Sprintf("schema.New: lru.New: %v", err))
	}
	return &lruCache{
		opts:         opts,
		entries:      lc,
		catalogSet:   make(map[string]struct{}),
		catalogLocks: make(map[string]*sync.Mutex),
		loads:        make(map[string]uint64),
		failed:       make(map[string]time.Time),
		metrics:      globalMetrics,
	}
}

// Get returns the Entry for key, loading the catalog on miss.
func (c *lruCache) Get(ctx context.Context, key Key) (*Entry, error) {
	if e, ok := c.entries.Get(key); ok {
		c.metrics.hit(key.Catalog)
		return e, nil
	}

	// Miss: load the catalog. The per-catalog lock plus an under-lock
	// re-check make concurrent misses a true singleflight: the first
	// loader populates the catalog, and the rest see that a load ended
	// while they waited and skip the redundant introspection.
	err := c.loadCatalogForKey(ctx, key, c.loadCount(key.Catalog))
	// The name comes from the caller. One that is no catalog gets no
	// label: a series per made-up name would grow without bound.
	if !errors.Is(err, ErrUnknownCatalog) {
		c.metrics.miss(key.Catalog)
	}
	if err != nil {
		return nil, err
	}
	if e, ok := c.entries.Get(key); ok {
		return e, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrUnknownTable, key)
}

// All returns a snapshot of every cached Entry ordered by Key.
func (c *lruCache) All() []*Entry {
	keys := c.entries.Keys()
	out := make([]*Entry, 0, len(keys))
	for _, k := range keys {
		if e, ok := c.entries.Peek(k); ok {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Key.String() < out[j].Key.String()
	})
	return out
}

// Invalidate drops a single entry.
func (c *lruCache) Invalidate(key Key) {
	c.entries.Remove(key)
}

// InvalidateCatalog drops every entry whose Catalog matches.
func (c *lruCache) InvalidateCatalog(catalog string) {
	for _, k := range c.entries.Keys() {
		if k.Catalog == catalog {
			c.entries.Remove(k)
		}
	}
	c.mu.Lock()
	delete(c.catalogSet, catalog)
	delete(c.failed, catalog)
	c.gen++
	c.mu.Unlock()
}

// InvalidateAll clears the cache and the known-catalog set.
func (c *lruCache) InvalidateAll() {
	c.entries.Purge()
	c.mu.Lock()
	c.catalogSet = make(map[string]struct{})
	c.failed = make(map[string]time.Time)
	c.gen++
	c.mu.Unlock()
}

// Load implements Cache.
func (c *lruCache) Load(ctx context.Context, catalogs []string) {
	for _, cat := range catalogs {
		if ctx.Err() != nil {
			return
		}
		if !c.needsLoad(cat) {
			continue
		}
		lock := c.lockForCatalog(cat)
		lock.Lock()
		// Another caller may have read the catalog while this one waited.
		if c.needsLoad(cat) {
			c.mu.Lock()
			gen := c.gen
			c.mu.Unlock()
			if err := c.reloadCatalogLocked(ctx, cat); err != nil {
				// No retry wait when the caller went away, which is no
				// failure of the data source, or when an invalidation
				// during the load asked for a fresh read.
				c.mu.Lock()
				if ctx.Err() == nil && c.gen == gen {
					c.failed[cat] = c.opts.Clock()
				}
				c.mu.Unlock()
				c.opts.Logger.WarnContext(ctx, "schema: load failed",
					slog.String("catalog", cat),
					slog.String("error", err.Error()),
				)
			}
		}
		lock.Unlock()
	}
}

// needsLoad reports whether Load has to read catalog: it was not read
// since the cache was last emptied, and no load of it failed within
// LoadRetry.
func (c *lruCache) needsLoad(catalog string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.catalogSet[catalog]; ok {
		return false
	}
	at, ok := c.failed[catalog]
	return !ok || c.opts.Clock().Sub(at) >= LoadRetry
}

// Refresh loads every catalog in catalogs (or every known catalog when
// catalogs is nil). Individual catalog failures are logged and counted;
// the first error is returned so callers can surface an aggregated
// failure without losing subsequent good loads.
func (c *lruCache) Refresh(ctx context.Context, catalogs []string) error {
	if catalogs == nil {
		c.mu.Lock()
		catalogs = make([]string, 0, len(c.catalogSet))
		for cat := range c.catalogSet {
			catalogs = append(catalogs, cat)
		}
		c.mu.Unlock()
	}

	var firstErr error
	for _, cat := range catalogs {
		if err := ctx.Err(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		if err := c.loadCatalogIfStale(ctx, cat); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			c.opts.Logger.WarnContext(ctx, "schema: refresh failed",
				slog.String("catalog", cat),
				slog.String("error", err.Error()),
			)
		}
	}
	return firstErr
}

// loadCatalogForKey loads key.Catalog on a Get miss. seen is the catalog's
// load count at the miss. Under the per-catalog lock it re-checks whether
// a concurrent miss loaded the catalog while we waited, and only then
// runs Loader.Load. Such a load is fresh also when it did not bring key
// (another letter case, no such table), so the check is on the load count
// and not on key alone. This dedups a burst of concurrent misses into a
// single introspection while still honouring Invalidate: without a load
// in between, an invalidated key reloads, and so does an emptied cache.
func (c *lruCache) loadCatalogForKey(ctx context.Context, key Key, seen uint64) error {
	lock := c.lockForCatalog(key.Catalog)
	lock.Lock()
	defer lock.Unlock()

	if c.entries.Contains(key) {
		return nil
	}
	if c.loadCount(key.Catalog) != seen && c.catalogHasEntries(key.Catalog) {
		return nil
	}
	return c.reloadCatalogLocked(ctx, key.Catalog)
}

// loadCount returns the number of completed loads of catalog.
func (c *lruCache) loadCount(catalog string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loads[catalog]
}

// loadCatalogIfStale skips the Load when every entry for catalog is
// fresher than TTL. Used by Refresh to avoid unnecessary introspection.
func (c *lruCache) loadCatalogIfStale(ctx context.Context, catalog string) error {
	lock := c.lockForCatalog(catalog)
	lock.Lock()
	defer lock.Unlock()

	if c.catalogHasEntries(catalog) && !c.catalogIsStale(catalog) {
		return nil
	}
	return c.reloadCatalogLocked(ctx, catalog)
}

// reloadCatalogLocked fetches the whole catalog schema and repopulates the
// cache for catalog. The caller must hold the per-catalog lock so a single
// Load is in flight per catalog.
func (c *lruCache) reloadCatalogLocked(ctx context.Context, catalog string) error {
	c.mu.Lock()
	gen := c.gen
	c.mu.Unlock()
	start := c.opts.Clock()
	sch, err := c.opts.Loader.Load(ctx, catalog)
	if !errors.Is(err, ErrUnknownCatalog) {
		c.metrics.observeRefresh(c.opts.Clock().Sub(start).Seconds())
	}
	if err != nil {
		c.markCatalogStale(catalog)
		return fmt.Errorf("%w: %w", ErrLoadFailed, err)
	}
	if sch.Catalog == "" {
		sch.Catalog = catalog
	}

	now := c.opts.Clock()
	// Add the loaded entries first and remove the dropped ones after. A
	// table that exists before and after is then never absent for a
	// concurrent reader.
	old := c.entries.Keys()
	loaded := make(map[Key]struct{})
	for _, ns := range sch.Schemas {
		for _, tbl := range ns.Tables {
			// Clone columns so later mutations to the driver's slice do
			// not affect us.
			cols := make([]pkgds.Column, len(tbl.Columns))
			copy(cols, tbl.Columns)
			e := &Entry{
				Key:     Key{Catalog: catalog, Schema: ns.Name, Table: tbl.Name},
				Columns: cols,
				Comment: tbl.Comments,
				Fetched: now,
			}
			c.entries.Add(e.Key, e)
			loaded[e.Key] = struct{}{}
		}
	}
	for _, k := range old {
		if _, ok := loaded[k]; !ok && k.Catalog == catalog {
			c.entries.Remove(k)
		}
	}

	c.mu.Lock()
	// An invalidation during the load may have removed a part of what
	// was added. Then the catalog does not count as read.
	if c.gen == gen {
		c.catalogSet[catalog] = struct{}{}
		delete(c.failed, catalog)
	}
	c.loads[catalog]++
	c.mu.Unlock()
	return nil
}

// lockForCatalog returns (creating if missing) the mutex guarding
// catalog loads.
func (c *lruCache) lockForCatalog(catalog string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m, ok := c.catalogLocks[catalog]; ok {
		return m
	}
	m := &sync.Mutex{}
	c.catalogLocks[catalog] = m
	return m
}

// catalogHasEntries reports whether at least one entry for catalog is
// currently cached.
func (c *lruCache) catalogHasEntries(catalog string) bool {
	for _, k := range c.entries.Keys() {
		if k.Catalog == catalog {
			return true
		}
	}
	return false
}

// catalogIsStale reports whether every entry for catalog has Fetched
// older than TTL. Mixed ages resolve to "not stale" — a single fresh
// entry is enough to skip the refresh.
func (c *lruCache) catalogIsStale(catalog string) bool {
	now := c.opts.Clock()
	anyFresh := false
	found := false
	for _, k := range c.entries.Keys() {
		if k.Catalog != catalog {
			continue
		}
		found = true
		if e, ok := c.entries.Peek(k); ok {
			if now.Sub(e.Fetched) < c.opts.TTL {
				anyFresh = true
				break
			}
		}
	}
	if !found {
		return true
	}
	return !anyFresh
}

// markCatalogStale flags every entry for catalog. Used when a refresh
// attempt failed — callers still see data but know it is stale.
func (c *lruCache) markCatalogStale(catalog string) {
	for _, k := range c.entries.Keys() {
		if k.Catalog != catalog {
			continue
		}
		if e, ok := c.entries.Peek(k); ok && !e.Stale {
			e.Stale = true
			c.entries.Add(k, e)
			c.metrics.stale(catalog)
		}
	}
}
