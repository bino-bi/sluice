// SPDX-License-Identifier: AGPL-3.0-or-later

package schema

import (
	"context"
	"fmt"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

// The catalog label of the miss counter comes from the caller, for example
// from the table name of describe_table. A name that is no catalog must
// not create a series.
func TestMissMetric_OnlyForACatalogThatLoads(t *testing.T) {
	misses := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_misses"}, []string{"catalog"})
	loader := &StaticLoader{Catalogs: map[string]pkgds.Schema{"pg": {Schemas: []pkgds.SchemaNS{{
		Name: "public", Tables: []pkgds.Table{{Name: "orders"}},
	}}}}}
	c := New(Options{Loader: loader}).(*lruCache)
	c.metrics = &metrics{misses: misses}
	ctx := context.Background()

	for i := range 5 {
		if _, err := c.Get(ctx, Key{Catalog: fmt.Sprintf("made-up-%d", i), Table: "t"}); err == nil {
			t.Fatal("a made-up catalog loaded")
		}
	}
	if _, err := c.Get(ctx, Key{Catalog: "pg", Schema: "public", Table: "orders"}); err != nil {
		t.Fatal(err)
	}

	ch := make(chan prometheus.Metric, 16)
	misses.Collect(ch)
	close(ch)
	if n := len(ch); n != 1 {
		t.Errorf("miss counter has %d series, want 1 (pg)", n)
	}
}
