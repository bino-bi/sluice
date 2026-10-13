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
	pkgds "github.com/bino-bi/sluice/pkg/datasource"
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
func (f *fakeSchemaCache) Load(context.Context, []string)          {}

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

// catalogLoader serves the same two tables for every catalog and counts
// the loads of each.
type catalogLoader struct {
	loads map[string]int
}

func (l *catalogLoader) Load(_ context.Context, catalog string) (pkgds.Schema, error) {
	l.loads[catalog]++
	return pkgds.Schema{Catalog: catalog, Schemas: []pkgds.SchemaNS{{
		Name:   "main",
		Tables: []pkgds.Table{{Name: "customers"}, {Name: "orders"}},
	}}}, nil
}

type fixedCatalogs []queryservice.CatalogInfo

func (f fixedCatalogs) List(context.Context) []queryservice.CatalogInfo { return f }

// newColdService has the catalogs shop and crm attached and an empty
// schema cache, as a server has after its start and after a reload.
func newColdService(t *testing.T, pol *denyingPolicy, down string) (*queryservice.Service, *catalogLoader) {
	t.Helper()
	loader := &catalogLoader{loads: map[string]int{}}
	return queryservice.New(queryservice.Options{
		Parser:     &fakeParser{},
		Policy:     pol,
		Rewriter:   &fakeRewriter{},
		Executor:   &fakeExecutor{},
		Audit:      &fakeAudit{},
		Schema:     schema.New(schema.Options{Loader: loader}),
		Catalogs:   func() []string { return []string{"shop", "crm"} },
		SourceDown: func(catalog string) bool { return catalog == down },
		Clock:      func() time.Time { return time.Unix(1713600000, 0) },
	}), loader
}

// The listing tools read the schema cache. Nothing fills it for a plain
// query, so they load a catalog themselves, once.
func TestListTables_LoadsTheCatalog(t *testing.T) {
	svc, loader := newColdService(t, &denyingPolicy{deny: map[string]bool{"customers": true}}, "")
	ctx := context.Background()
	for range 2 {
		got, _, err := svc.ListTables(ctx, "shop", "", nil, queryservice.Page{})
		if err != nil {
			t.Fatalf("ListTables: %v", err)
		}
		if len(got) != 1 || got[0].Table != "orders" {
			t.Fatalf("tables = %v, want only shop.main.orders", got)
		}
	}
	if loader.loads["shop"] != 1 || loader.loads["crm"] != 0 {
		t.Errorf("loads = %v, want one load of shop only", loader.loads)
	}
	// A name that is no attached catalog reaches no data source.
	if got, _, err := svc.ListTables(ctx, "nope", "", nil, queryservice.Page{}); err != nil || len(got) != 0 {
		t.Errorf("ListTables(nope) = %v, %v, want no table", got, err)
	}
	if loader.loads["nope"] != 0 {
		t.Errorf("an unknown catalog name was loaded %d time(s)", loader.loads["nope"])
	}
}

func TestAccessibleTables_LoadsEveryCatalogThatIsUp(t *testing.T) {
	svc, loader := newColdService(t, &denyingPolicy{}, "crm")
	got, _, err := svc.AccessibleTables(context.Background(), nil, "", queryservice.Page{})
	if err != nil {
		t.Fatalf("AccessibleTables: %v", err)
	}
	if len(got) != 2 || got[0].Catalog != "shop" || got[1].Catalog != "shop" {
		t.Errorf("tables = %v, want the two tables of shop", got)
	}
	if loader.loads["crm"] != 0 {
		t.Errorf("the data source that is down was loaded %d time(s)", loader.loads["crm"])
	}
}

func TestListCatalogs_HidesADeniedCatalogOnAColdCache(t *testing.T) {
	svc, _ := newColdService(t, &denyingPolicy{deny: map[string]bool{"customers": true, "orders": true}}, "crm")
	got, err := svc.ListCatalogs(context.Background(),
		fixedCatalogs{{Name: "shop"}, {Name: "crm"}}, nil)
	if err != nil {
		t.Fatalf("ListCatalogs: %v", err)
	}
	// shop: every table denied. crm: down, so nothing is known about it.
	if len(got) != 1 || got[0].Name != "crm" {
		t.Errorf("catalogs = %v, want only crm", got)
	}
}

// actionPolicy allows only a request that names the SELECT action, as an
// allow with `actions: [SELECT]` does.
type actionPolicy struct{ denyingPolicy }

func (p *actionPolicy) Evaluate(_ context.Context, in policy.Input) (*policy.Decision, error) {
	if policy.ActionOf(in) != pkgapi.ActionSelect {
		return &policy.Decision{Outcome: policy.OutcomeDeny}, nil
	}
	return &policy.Decision{Outcome: policy.OutcomeAllow}, nil
}

// The listing asks whether the subject may read a table, so it must ask
// as a SELECT. Without an action, a policy with actions never matches.
func TestListTables_VisibilityIsCheckedAsSelect(t *testing.T) {
	svc := queryservice.New(queryservice.Options{
		Parser:   &fakeParser{},
		Policy:   &actionPolicy{},
		Rewriter: &fakeRewriter{},
		Executor: &fakeExecutor{},
		Audit:    &fakeAudit{},
		Schema: &fakeSchemaCache{entries: []*schema.Entry{
			{Key: schema.Key{Catalog: "shop", Schema: "main", Table: "orders"}},
		}},
		Clock: func() time.Time { return time.Unix(1713600000, 0) },
	})
	got, _, err := svc.ListTables(context.Background(), "shop", "", nil, queryservice.Page{})
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("tables = %v, want shop.main.orders", got)
	}
}

// recordingPolicy keeps the input of the last Explain call.
type recordingPolicy struct {
	denyingPolicy
	explained policy.Input
}

func (p *recordingPolicy) Explain(_ context.Context, in policy.Input) (*pkgapi.ExplainResult, error) {
	p.explained = in
	return &pkgapi.ExplainResult{}, nil
}

// Explain for a table without SQL asks about a read, like the list tools.
func TestExplain_TableOnlyIsARead(t *testing.T) {
	pol := &recordingPolicy{}
	svc := queryservice.New(queryservice.Options{
		Parser:   &fakeParser{},
		Policy:   pol,
		Rewriter: &fakeRewriter{},
		Executor: &fakeExecutor{},
		Audit:    &fakeAudit{},
		Clock:    func() time.Time { return time.Unix(1713600000, 0) },
	})
	_, err := svc.Explain(context.Background(), queryservice.ExplainInput{
		Table: parser.TableRef{Catalog: "shop", Schema: "main", Table: "orders"},
	})
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if got := policy.ActionOf(pol.explained); got != pkgapi.ActionSelect {
		t.Errorf("action = %q, want SELECT", got)
	}
}
