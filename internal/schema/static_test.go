// SPDX-License-Identifier: AGPL-3.0-or-later

package schema

import (
	"context"
	"errors"
	"testing"

	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

func TestStaticCache(t *testing.T) {
	key := Key{Catalog: "pg", Schema: "public", Table: "orders"}
	c := NewStatic([]*Entry{{
		Key:     key,
		Columns: []pkgds.Column{{Name: "id"}, {Name: "tenant_id"}},
	}})

	e, err := c.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get hit: %v", err)
	}
	if len(e.Columns) != 2 || e.Columns[0].Name != "id" {
		t.Fatalf("unexpected entry: %+v", e)
	}

	_, err = c.Get(context.Background(), Key{Catalog: "pg", Schema: "public", Table: "missing"})
	if !errors.Is(err, ErrUnknownTable) {
		t.Fatalf("Get miss: want ErrUnknownTable, got %v", err)
	}

	if got := len(c.All()); got != 1 {
		t.Fatalf("All: want 1 entry, got %d", got)
	}
	c.Invalidate(key)
	c.InvalidateCatalog("pg")
	c.InvalidateAll()
	if err := c.Refresh(context.Background(), []string{"pg"}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := len(c.All()); got != 1 {
		t.Fatalf("invalidation must be a no-op on the static cache, got %d entries", got)
	}
}
