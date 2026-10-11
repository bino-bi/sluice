// SPDX-License-Identifier: AGPL-3.0-or-later

package schema_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pkgds "github.com/bino-bi/sluice/pkg/datasource"

	"github.com/bino-bi/sluice/internal/schema"
)

// fakeLoader is a Loader that records call counts and can be primed with
// successes and failures per catalog.
type fakeLoader struct {
	mu       sync.Mutex
	calls    map[string]int
	catalogs map[string]pkgds.Schema
	failOnce map[string]error
}

func newFakeLoader() *fakeLoader {
	return &fakeLoader{
		calls:    map[string]int{},
		catalogs: map[string]pkgds.Schema{},
		failOnce: map[string]error{},
	}
}

func (f *fakeLoader) set(cat string, sch pkgds.Schema) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.catalogs[cat] = sch
}

func (f *fakeLoader) primeErr(cat string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failOnce[cat] = err
}

func (f *fakeLoader) Load(_ context.Context, catalog string) (pkgds.Schema, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[catalog]++
	if err, ok := f.failOnce[catalog]; ok {
		delete(f.failOnce, catalog)
		return pkgds.Schema{}, err
	}
	if sch, ok := f.catalogs[catalog]; ok {
		return sch, nil
	}
	return pkgds.Schema{}, schema.ErrUnknownCatalog
}

func (f *fakeLoader) callCount(cat string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[cat]
}

func sampleSchema(cat string) pkgds.Schema {
	return pkgds.Schema{
		Catalog: cat,
		Schemas: []pkgds.SchemaNS{
			{
				Name: "public",
				Tables: []pkgds.Table{
					{Name: "orders", Columns: []pkgds.Column{
						{Name: "id", SQLType: "bigint", Position: 1},
						{Name: "customer_id", SQLType: "bigint", Position: 2},
					}},
					{Name: "customers", Columns: []pkgds.Column{
						{Name: "id", SQLType: "bigint", Position: 1},
						{Name: "email", SQLType: "text", Position: 2, Comment: "@pii:email"},
					}},
				},
			},
		},
	}
}

func TestCacheHitAfterFirstMiss(t *testing.T) {
	loader := newFakeLoader()
	loader.set("pg", sampleSchema("pg"))
	c := schema.New(schema.Options{Loader: loader})

	key := schema.Key{Catalog: "pg", Schema: "public", Table: "orders"}
	for i := range 5 {
		if _, err := c.Get(context.Background(), key); err != nil {
			t.Fatalf("Get #%d: %v", i, err)
		}
	}
	if n := loader.callCount("pg"); n != 1 {
		t.Errorf("Load called %d times; want exactly 1", n)
	}
}

func TestCacheMissUnknownTable(t *testing.T) {
	loader := newFakeLoader()
	loader.set("pg", sampleSchema("pg"))
	c := schema.New(schema.Options{Loader: loader})

	_, err := c.Get(context.Background(), schema.Key{Catalog: "pg", Schema: "public", Table: "ghosts"})
	if !errors.Is(err, schema.ErrUnknownTable) {
		t.Fatalf("err = %v; want ErrUnknownTable", err)
	}
}

func TestCacheUnknownCatalogPropagates(t *testing.T) {
	loader := newFakeLoader()
	c := schema.New(schema.Options{Loader: loader})

	_, err := c.Get(context.Background(), schema.Key{Catalog: "nosuch", Schema: "public", Table: "orders"})
	if !errors.Is(err, schema.ErrLoadFailed) {
		t.Fatalf("err = %v; want ErrLoadFailed", err)
	}
	if !errors.Is(err, schema.ErrUnknownCatalog) {
		t.Fatalf("err = %v; should also wrap ErrUnknownCatalog", err)
	}
}

func TestCacheInvalidate(t *testing.T) {
	loader := newFakeLoader()
	loader.set("pg", sampleSchema("pg"))
	c := schema.New(schema.Options{Loader: loader})

	key := schema.Key{Catalog: "pg", Schema: "public", Table: "orders"}
	if _, err := c.Get(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	c.Invalidate(key)
	if _, err := c.Get(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	// Individual Invalidate forces the catalog to re-introspect on the
	// next miss, so Load is called twice.
	if n := loader.callCount("pg"); n != 2 {
		t.Errorf("Load called %d times; want 2 (initial + after-invalidate)", n)
	}
}

func TestCacheInvalidateCatalogForcesReload(t *testing.T) {
	loader := newFakeLoader()
	loader.set("pg", sampleSchema("pg"))
	c := schema.New(schema.Options{Loader: loader})

	key := schema.Key{Catalog: "pg", Schema: "public", Table: "orders"}
	if _, err := c.Get(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	c.InvalidateCatalog("pg")
	if _, err := c.Get(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if n := loader.callCount("pg"); n != 2 {
		t.Errorf("Load called %d times; want 2", n)
	}
}

func TestCacheInvalidateAll(t *testing.T) {
	loader := newFakeLoader()
	loader.set("pg", sampleSchema("pg"))
	loader.set("mysql", sampleSchema("mysql"))
	c := schema.New(schema.Options{Loader: loader})

	ctx := context.Background()
	_, _ = c.Get(ctx, schema.Key{Catalog: "pg", Schema: "public", Table: "orders"})
	_, _ = c.Get(ctx, schema.Key{Catalog: "mysql", Schema: "public", Table: "orders"})
	c.InvalidateAll()
	if len(c.All()) != 0 {
		t.Fatalf("All() after InvalidateAll = %d entries; want 0", len(c.All()))
	}
}

func TestCacheConcurrentMissesDedupe(t *testing.T) {
	loader := newFakeLoader()
	loader.set("pg", sampleSchema("pg"))
	c := schema.New(schema.Options{Loader: loader})

	key := schema.Key{Catalog: "pg", Schema: "public", Table: "orders"}
	var wg sync.WaitGroup
	var errs atomic.Int32
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Get(context.Background(), key); err != nil {
				errs.Add(1)
			}
		}()
	}
	wg.Wait()
	if errs.Load() != 0 {
		t.Errorf("Get errored on %d goroutines", errs.Load())
	}
	if n := loader.callCount("pg"); n != 1 {
		t.Errorf("Load called %d times under contention; want 1", n)
	}
}

func TestCacheAllIsSorted(t *testing.T) {
	loader := newFakeLoader()
	loader.set("zeta", sampleSchema("zeta"))
	loader.set("alpha", sampleSchema("alpha"))
	c := schema.New(schema.Options{Loader: loader})

	if err := c.Refresh(context.Background(), []string{"zeta", "alpha"}); err != nil {
		t.Fatal(err)
	}
	got := c.All()
	if len(got) == 0 {
		t.Fatal("All() is empty")
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Key.String() > got[i].Key.String() {
			t.Fatalf("All() not sorted: %s > %s", got[i-1].Key, got[i].Key)
		}
	}
}

func TestCacheRefreshServesStaleOnFailure(t *testing.T) {
	loader := newFakeLoader()
	loader.set("pg", sampleSchema("pg"))

	// Frozen clock so TTL is controllable.
	now := time.Now()
	clock := func() time.Time { return now }

	c := schema.New(schema.Options{
		Loader: loader,
		TTL:    time.Minute,
		Clock:  clock,
	})

	ctx := context.Background()
	if _, err := c.Get(ctx, schema.Key{Catalog: "pg", Schema: "public", Table: "orders"}); err != nil {
		t.Fatal(err)
	}

	// Simulate failure on next refresh and advance clock past TTL.
	loader.primeErr("pg", errors.New("network down"))
	now = now.Add(2 * time.Minute)
	if err := c.Refresh(ctx, []string{"pg"}); err == nil {
		t.Fatal("Refresh returned nil on failure")
	}

	// Entry should still be served and marked Stale.
	e, err := c.Get(ctx, schema.Key{Catalog: "pg", Schema: "public", Table: "orders"})
	if err != nil {
		t.Fatalf("Get after stale refresh: %v", err)
	}
	if !e.Stale {
		t.Error("Entry.Stale = false; want true after failed refresh")
	}
}

func TestCacheRefreshEmptyCatalogsUsesKnownSet(t *testing.T) {
	loader := newFakeLoader()
	loader.set("pg", sampleSchema("pg"))
	c := schema.New(schema.Options{Loader: loader})

	// Prime the known-catalog set with a Get.
	ctx := context.Background()
	if _, err := c.Get(ctx, schema.Key{Catalog: "pg", Schema: "public", Table: "orders"}); err != nil {
		t.Fatal(err)
	}
	before := loader.callCount("pg")
	if err := c.Refresh(ctx, nil); err != nil {
		t.Fatal(err)
	}
	// Refresh won't re-load because entries are still fresh. That's
	// correct — TTL wasn't exceeded. Call count stays the same.
	if loader.callCount("pg") != before {
		t.Errorf("Refresh reloaded a fresh catalog (calls %d → %d)", before, loader.callCount("pg"))
	}
}

func TestNewPanicsWithoutLoader(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic")
		}
	}()
	_ = schema.New(schema.Options{})
}

// gatedLoader blocks every Load until release is closed. With firstEmpty
// the first Load returns a catalog without tables.
type gatedLoader struct {
	release    chan struct{}
	calls      atomic.Int32
	sch        pkgds.Schema
	firstEmpty bool
}

func (g *gatedLoader) Load(context.Context, string) (pkgds.Schema, error) {
	n := g.calls.Add(1)
	<-g.release
	if g.firstEmpty && n == 1 {
		return pkgds.Schema{}, nil
	}
	return g.sch, nil
}

// A lookup whose key differs from the cached one only in case, or names a
// table that does not exist, misses also after a load. Requests that
// waited for that load must not each run their own.
func TestCacheConcurrentMissesOfUncachedKeyDedupe(t *testing.T) {
	loader := &gatedLoader{release: make(chan struct{}), sch: pkgds.Schema{Schemas: []pkgds.SchemaNS{{
		Name:   "main",
		Tables: []pkgds.Table{{Name: "Customers", Columns: []pkgds.Column{{Name: "id"}}}},
	}}}}
	c := schema.New(schema.Options{Loader: loader})

	const n = 20
	var started, done sync.WaitGroup
	for range n {
		started.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			started.Done()
			_, err := c.Get(context.Background(), schema.Key{Catalog: "shop", Schema: "main", Table: "customers"})
			if !errors.Is(err, schema.ErrUnknownTable) {
				t.Errorf("err = %v, want ErrUnknownTable", err)
			}
		}()
	}
	started.Wait()
	// Let every goroutine reach the catalog lock before the first load ends.
	time.Sleep(50 * time.Millisecond)
	close(loader.release)
	done.Wait()

	if got := loader.calls.Load(); got != 1 {
		t.Errorf("Load called %d times for %d concurrent misses, want 1", got, n)
	}
	if len(c.All()) != 1 {
		t.Errorf("All() = %d entries, want the loaded table", len(c.All()))
	}
}

// A request that waited for a load must not skip its own when that load
// left the catalog without entries, as InvalidateAll does after a load.
// The first load here returns no tables, so the waiter must load again.
func TestCacheWaiterReloadsEmptyCatalog(t *testing.T) {
	loader := &gatedLoader{release: make(chan struct{}), firstEmpty: true, sch: pkgds.Schema{Schemas: []pkgds.SchemaNS{{
		Name:   "main",
		Tables: []pkgds.Table{{Name: "customers", Columns: []pkgds.Column{{Name: "id"}}}},
	}}}}
	c := schema.New(schema.Options{Loader: loader})

	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := c.Get(context.Background(), schema.Key{Catalog: "shop", Schema: "main", Table: "customers"})
			errs <- err
		}()
	}
	// Let both goroutines reach the catalog lock before the first load ends.
	time.Sleep(50 * time.Millisecond)
	close(loader.release)

	var found, unknown int
	for range 2 {
		switch err := <-errs; {
		case err == nil:
			found++
		case errors.Is(err, schema.ErrUnknownTable):
			unknown++
		default:
			t.Errorf("Get: %v", err)
		}
	}
	if found != 1 || unknown != 1 {
		t.Errorf("%d lookups found the table and %d did not, want 1 and 1", found, unknown)
	}
	if got := loader.calls.Load(); got != 2 {
		t.Errorf("Load called %d times, want 2", got)
	}
}

// A miss after InvalidateAll loads the catalog again.
func TestCacheMissAfterInvalidateAllReloads(t *testing.T) {
	loader := newFakeLoader()
	loader.set("pg", sampleSchema("pg"))
	c := schema.New(schema.Options{Loader: loader})
	ctx := context.Background()
	key := schema.Key{Catalog: "pg", Schema: "public", Table: "orders"}
	if _, err := c.Get(ctx, key); err != nil {
		t.Fatal(err)
	}
	c.InvalidateAll()
	if _, err := c.Get(ctx, key); err != nil {
		t.Fatalf("Get after InvalidateAll: %v", err)
	}
	if n := loader.callCount("pg"); n != 2 {
		t.Errorf("Load called %d times, want 2", n)
	}
}

// A reload must not take a table that still exists out of the cache, not
// even for a moment: a reader would see a masked table as unknown.
func TestCacheReloadKeepsEntriesVisible(t *testing.T) {
	loader := newFakeLoader()
	loader.set("pg", sampleSchema("pg"))
	c := schema.New(schema.Options{Loader: loader})
	ctx := context.Background()
	if _, err := c.Get(ctx, schema.Key{Catalog: "pg", Schema: "public", Table: "orders"}); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var missing atomic.Int32
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if len(c.All()) != 2 {
				missing.Add(1)
			}
		}
	}()
	// Every miss of an unknown table reloads the catalog.
	for range 2000 {
		_, _ = c.Get(ctx, schema.Key{Catalog: "pg", Schema: "public", Table: "ghosts"})
	}
	close(stop)
	wg.Wait()
	if n := missing.Load(); n != 0 {
		t.Errorf("All() lost a table during a reload %d times", n)
	}
}

// A table dropped at the source leaves the cache on the next load.
func TestCacheReloadDropsRemovedTable(t *testing.T) {
	loader := newFakeLoader()
	loader.set("pg", sampleSchema("pg"))
	c := schema.New(schema.Options{Loader: loader})
	ctx := context.Background()
	if _, err := c.Get(ctx, schema.Key{Catalog: "pg", Schema: "public", Table: "orders"}); err != nil {
		t.Fatal(err)
	}
	sch := sampleSchema("pg")
	sch.Schemas[0].Tables = sch.Schemas[0].Tables[:1] // orders only
	loader.set("pg", sch)
	_, _ = c.Get(ctx, schema.Key{Catalog: "pg", Schema: "public", Table: "ghosts"})
	if got := c.All(); len(got) != 1 || got[0].Key.Table != "orders" {
		t.Errorf("All() = %v, want orders only", got)
	}
}
