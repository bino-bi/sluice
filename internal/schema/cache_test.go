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
