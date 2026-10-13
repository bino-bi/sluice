// SPDX-License-Identifier: AGPL-3.0-or-later

package schema

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

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

// failingLoader knows the catalog and cannot read it, as for a data source
// that is down.
type failingLoader struct{}

func (failingLoader) Load(context.Context, string) (pkgds.Schema, error) {
	return pkgds.Schema{}, errors.New("connection refused")
}

func histogramCount(t *testing.T, h prometheus.Histogram) uint64 {
	t.Helper()
	var m dto.Metric
	if err := h.Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetHistogram().GetSampleCount()
}

// A miss on a catalog that exists and cannot be read still counts: the
// operator needs the signal per catalog. Each read is timed once, a name
// that is no catalog is not.
func TestMetrics_FailedReadOfAKnownCatalog(t *testing.T) {
	misses := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_misses"}, []string{"catalog"})
	refresh := prometheus.NewHistogram(prometheus.HistogramOpts{Name: "test_refresh"})
	c := New(Options{Loader: failingLoader{}}).(*lruCache)
	c.metrics = &metrics{misses: misses, refresh: refresh}
	ctx := context.Background()

	if _, err := c.Get(ctx, Key{Catalog: "pg", Schema: "public", Table: "orders"}); err == nil {
		t.Fatal("the read did not fail")
	}
	ch := make(chan prometheus.Metric, 4)
	misses.Collect(ch)
	close(ch)
	if n := len(ch); n != 1 {
		t.Errorf("miss counter has %d series, want 1 for the catalog that is down", n)
	}
	if got := histogramCount(t, refresh); got != 1 {
		t.Errorf("timed reads = %d, want 1", got)
	}

	known := New(Options{Loader: &StaticLoader{Catalogs: map[string]pkgds.Schema{"pg": {}}}}).(*lruCache)
	known.metrics = &metrics{refresh: refresh}
	known.Load(ctx, []string{"pg", "made-up"})
	if got := histogramCount(t, refresh); got != 2 {
		t.Errorf("timed reads = %d, want 2: one more for pg, none for the made-up name", got)
	}
}
