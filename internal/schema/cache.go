// SPDX-License-Identifier: AGPL-3.0-or-later

package schema

import (
	"context"
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

	// InvalidateCatalog evicts every entry belonging to catalog. Called
	// after a DataSource is reattached.
	InvalidateCatalog(catalog string)

	// InvalidateAll drops every entry. Used by admin /reload before a
	// wholesale policy + data source rebuild.
	InvalidateAll()

	// Refresh loads every known catalog. On first call it also primes the
	// list of known catalogs. Errors are aggregated; callers log them but
	// always see the most recent good data.
	Refresh(ctx context.Context, catalogs []string) error
}

// Options configures a Cache. Nil-valued functions fall back to
// reasonable defaults; the only required field is Loader.
type Options struct {
	// Loader is required. Cache panics on New when nil.
	Loader Loader

	// TTL is the entry lifetime; entries older than TTL become candidates
	// for refresh. Zero means DefaultTTL.
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
	// DefaultTTL is 10 minutes. A running instance with ~1k tables pays
	// one lightweight information_schema scan per catalog per TTL.
	DefaultTTL = 10 * time.Minute

	// DefaultCapacity matches the performance budget in
	// concept §4.12: 10 000 tables per instance.
	DefaultCapacity = 10_000
)

// lruCache is the only Cache implementation in MVP.
type lruCache struct {
	opts    Options
	entries *lru.Cache[Key, *Entry]

	// catalogSet is the set of catalogs the cache has ever loaded. Used
	// by Refresh when callers don't pass an explicit list. Protected by
	// mu.
	mu           sync.Mutex
	catalogSet   map[string]struct{}
	catalogLocks map[string]*sync.Mutex // per-catalog load dedup

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
		metrics:      globalMetrics,
	}
}

// Get returns the Entry for key, loading the catalog on miss.
func (c *lruCache) Get(ctx context.Context, key Key) (*Entry, error) {
	if e, ok := c.entries.Get(key); ok {
		c.metrics.hit(key.Catalog)
		return e, nil
	}
	c.metrics.miss(key.Catalog)

	// Miss: force a catalog reload. Per-catalog lock dedups concurrent
	// misses (multiple Gets for different tables in the same catalog
	// share a single introspection).
	if err := c.loadCatalogForce(ctx, key.Catalog); err != nil {
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
	c.mu.Unlock()
}

// InvalidateAll clears the cache and the known-catalog set.
func (c *lruCache) InvalidateAll() {
	c.entries.Purge()
	c.mu.Lock()
	c.catalogSet = make(map[string]struct{})
	c.mu.Unlock()
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

	start := c.opts.Clock()
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
	c.metrics.observeRefresh(c.opts.Clock().Sub(start).Seconds())
	return firstErr
}

// loadCatalogForce always runs Loader.Load (still dedups concurrent
// callers via the per-catalog lock). Used by Get misses where we know
// the caller needs a fresh answer.
func (c *lruCache) loadCatalogForce(ctx context.Context, catalog string) error {
	return c.loadCatalog(ctx, catalog, true)
}

// loadCatalogIfStale skips the Load when every entry for catalog is
// fresher than TTL. Used by Refresh to avoid unnecessary introspection.
func (c *lruCache) loadCatalogIfStale(ctx context.Context, catalog string) error {
	return c.loadCatalog(ctx, catalog, false)
}

// loadCatalog fetches the whole catalog schema and caches every table.
// Per-catalog serialisation (lockForCatalog) guarantees a single
// in-flight Load call per catalog; concurrent Gets for different tables
// in the same catalog share that one introspection. When force is
// false the Load is skipped if the cache already holds fresh data.
func (c *lruCache) loadCatalog(ctx context.Context, catalog string, force bool) error {
	lock := c.lockForCatalog(catalog)
	lock.Lock()
	defer lock.Unlock()

	if !force && c.catalogHasEntries(catalog) && !c.catalogIsStale(catalog) {
		return nil
	}

	sch, err := c.opts.Loader.Load(ctx, catalog)
	if err != nil {
		c.markCatalogStale(catalog)
		return fmt.Errorf("%w: %w", ErrLoadFailed, err)
	}
	if sch.Catalog == "" {
		sch.Catalog = catalog
	}

	now := c.opts.Clock()
	// Remove stale entries for catalog, then repopulate.
	for _, k := range c.entries.Keys() {
		if k.Catalog == catalog {
			c.entries.Remove(k)
		}
	}
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
		}
	}

	c.mu.Lock()
	c.catalogSet[catalog] = struct{}{}
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
