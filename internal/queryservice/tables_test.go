// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/internal/schema"
	pkgapi "github.com/bino-bi/sluice/pkg/apitypes"
)

// fakeSchemaCache serves a fixed entry set; only All is exercised by the
// listing paths.
type fakeSchemaCache struct {
	entries []*schema.Entry
}

func (f *fakeSchemaCache) Get(_ context.Context, key schema.Key) (*schema.Entry, error) {
	for _, e := range f.entries {
		if e.Key == key {
			return e, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", schema.ErrUnknownTable, key)
}
func (f *fakeSchemaCache) All() []*schema.Entry                    { return f.entries }
func (f *fakeSchemaCache) Invalidate(schema.Key)                   {}
func (f *fakeSchemaCache) InvalidateCatalog(string)                {}
func (f *fakeSchemaCache) InvalidateAll()                          {}
func (f *fakeSchemaCache) Refresh(context.Context, []string) error { return nil }

// denyingPolicy denies the tables named in deny, allows everything else.
type denyingPolicy struct {
	deny map[string]bool
}

func (p *denyingPolicy) Evaluate(_ context.Context, in policy.Input) (*policy.Decision, error) {
	for _, t := range in.Tables {
		if p.deny[t.Table] {
			return &policy.Decision{Outcome: policy.OutcomeDeny}, nil
		}
	}
	return &policy.Decision{Outcome: policy.OutcomeAllow}, nil
}
func (p *denyingPolicy) Explain(context.Context, policy.Input) (*pkgapi.ExplainResult, error) {
	return &pkgapi.ExplainResult{}, nil
}

func newListingService(t *testing.T, tables int, deny map[string]bool) *queryservice.Service {
	t.Helper()
	entries := make([]*schema.Entry, 0, tables)
	for i := range tables {
		entries = append(entries, &schema.Entry{
			Key: schema.Key{Catalog: "shop", Schema: "main", Table: fmt.Sprintf("t%03d", i)},
		})
	}
	return queryservice.New(queryservice.Options{
		Parser:   &fakeParser{},
		Policy:   &denyingPolicy{deny: deny},
		Rewriter: &fakeRewriter{},
		Executor: &fakeExecutor{},
		Audit:    &fakeAudit{},
		Schema:   &fakeSchemaCache{entries: entries},
		Clock:    func() time.Time { return time.Unix(1713600000, 0) },
	})
}

func TestListTables_PagedWalkMatchesUnpaginated(t *testing.T) {
	deny := map[string]bool{"t003": true, "t007": true}
	svc := newListingService(t, 25, deny)
	ctx := context.Background()

	full, next, err := svc.ListTables(ctx, "shop", "", nil, queryservice.Page{Limit: queryservice.MaxListLimit})
	if err != nil {
		t.Fatalf("full list: %v", err)
	}
	if next != "" {
		t.Fatalf("full list should be exhausted, got cursor %q", next)
	}
	if len(full) != 23 {
		t.Fatalf("full list len = %d want 23", len(full))
	}

	var walked []parser.TableRef
	cursor := ""
	pages := 0
	for {
		refs, nc, err := svc.ListTables(ctx, "shop", "", nil, queryservice.Page{Limit: 4, After: cursor})
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		walked = append(walked, refs...)
		pages++
		if pages > 30 {
			t.Fatal("cursor walk did not terminate")
		}
		if nc == "" {
			break
		}
		cursor = nc
	}
	if len(walked) != len(full) {
		t.Fatalf("walked %d tables, want %d", len(walked), len(full))
	}
	for i := range full {
		if walked[i] != full[i] {
			t.Fatalf("page walk diverged at %d: got %v want %v", i, walked[i], full[i])
		}
	}
	// Denied tables never appear and never consume page slots.
	for _, r := range walked {
		if deny[r.Table] {
			t.Fatalf("denied table %s listed", r.Table)
		}
	}
}

func TestListTables_LimitClamping(t *testing.T) {
	svc := newListingService(t, 30, nil)
	ctx := context.Background()

	refs, next, err := svc.ListTables(ctx, "shop", "", nil, queryservice.Page{Limit: -5})
	if err != nil {
		t.Fatalf("default limit: %v", err)
	}
	if len(refs) != 30 || next != "" {
		t.Fatalf("limit<=0 should use default (500): got %d refs cursor %q", len(refs), next)
	}

	refs, next, err = svc.ListTables(ctx, "shop", "", nil, queryservice.Page{Limit: 10})
	if err != nil {
		t.Fatalf("limit 10: %v", err)
	}
	if len(refs) != 10 || next == "" {
		t.Fatalf("limit 10: got %d refs, cursor %q", len(refs), next)
	}
}

func TestListTables_CursorPastEnd(t *testing.T) {
	svc := newListingService(t, 5, nil)
	refs, next, err := svc.ListTables(context.Background(), "shop", "", nil,
		queryservice.Page{Limit: 10, After: "shop.main.t999"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(refs) != 0 || next != "" {
		t.Fatalf("past-end cursor: got %d refs cursor %q", len(refs), next)
	}
}

func TestAccessibleTables_CatalogFilterAndPaging(t *testing.T) {
	svc := newListingService(t, 8, map[string]bool{"t000": true})
	ctx := context.Background()

	refs, next, err := svc.AccessibleTables(ctx, nil, "shop", queryservice.Page{Limit: 5})
	if err != nil {
		t.Fatalf("accessible: %v", err)
	}
	if len(refs) != 5 || next == "" {
		t.Fatalf("page 1: got %d refs cursor %q", len(refs), next)
	}
	refs2, next2, err := svc.AccessibleTables(ctx, nil, "shop", queryservice.Page{Limit: 5, After: next})
	if err != nil {
		t.Fatalf("accessible page 2: %v", err)
	}
	if len(refs2) != 2 || next2 != "" {
		t.Fatalf("page 2: got %d refs cursor %q", len(refs2), next2)
	}

	none, _, err := svc.AccessibleTables(ctx, nil, "nope", queryservice.Page{})
	if err != nil {
		t.Fatalf("unknown catalog: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("unknown catalog returned %d refs", len(none))
	}
}
