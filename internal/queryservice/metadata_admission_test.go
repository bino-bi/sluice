// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice_test

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/internal/schema"
	pkgapi "github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// factsPolicy allows everything and counts the evaluations and the
// requests that carried request facts.
type factsPolicy struct {
	evals     atomic.Int64
	withFacts atomic.Int64
}

func (p *factsPolicy) Evaluate(_ context.Context, in policy.Input) (*policy.Decision, error) {
	p.evals.Add(1)
	if in.Request != nil {
		p.withFacts.Add(1)
	}
	return &policy.Decision{Outcome: policy.OutcomeAllow}, nil
}

func (p *factsPolicy) Explain(_ context.Context, in policy.Input) (*pkgapi.ExplainResult, error) {
	if in.Request != nil {
		p.withFacts.Add(1)
	}
	return &pkgapi.ExplainResult{}, nil
}

// countingParser counts Parse calls.
type countingParser struct {
	fakeParser
	calls atomic.Int64
}

func (p *countingParser) Parse(ctx context.Context, sql string) (parser.AST, error) {
	p.calls.Add(1)
	return p.fakeParser.Parse(ctx, sql)
}

func metadataService(pol *factsPolicy, p parser.Parser, rl *denyingRateLimiter, catalogs, tables int) *queryservice.Service {
	var entries []*schema.Entry
	for c := range catalogs {
		for i := range tables {
			entries = append(entries, &schema.Entry{Key: schema.Key{
				Catalog: fmt.Sprintf("c%d", c), Schema: "main", Table: fmt.Sprintf("t%03d", i),
			}})
		}
	}
	opts := queryservice.Options{
		Parser:   p,
		Policy:   pol,
		Rewriter: &fakeRewriter{},
		Executor: &fakeExecutor{},
		Audit:    &fakeAudit{},
		Schema:   &fakeSchemaCache{entries: entries},
	}
	if rl != nil {
		opts.RateLimiter = rl
	}
	return queryservice.New(opts)
}

// The metadata calls pass the same admission as a query: a subject over
// its rate limit gets ERR_RATE_LIMITED before any policy evaluation.
func TestMetadata_PassAdmission(t *testing.T) {
	pol := &factsPolicy{}
	svc := metadataService(pol, &fakeParser{}, &denyingRateLimiter{}, 1, 3)
	ctx := context.Background()
	user := &identity.UserCtx{Subject: "alice"}
	ref := parser.TableRef{Catalog: "c0", Schema: "main", Table: "t000"}

	calls := map[string]func() error{
		"ListCatalogs": func() error {
			_, err := svc.ListCatalogs(ctx, fixedCatalogs{{Name: "c0"}}, user, nil)
			return err
		},
		"ListTables": func() error {
			_, _, err := svc.ListTables(ctx, "c0", "", user, nil, queryservice.Page{})
			return err
		},
		"AccessibleTables": func() error {
			_, _, err := svc.AccessibleTables(ctx, user, nil, "", queryservice.Page{})
			return err
		},
		"DescribeTable": func() error {
			_, err := svc.DescribeTable(ctx, ref, user, nil)
			return err
		},
		"Explain": func() error {
			_, err := svc.Explain(ctx, queryservice.ExplainInput{User: user, Table: ref, Origin: queryservice.OriginMCP})
			return err
		},
		"Sample": func() error {
			_, err := svc.Sample(ctx, queryservice.SampleRequest{Table: "c0.main.t000", Rows: 5, User: user})
			return err
		},
	}
	for name, call := range calls {
		if ae := pkgerr.FromError(call()); ae == nil || ae.Code != pkgerr.CodeRateLimited {
			t.Errorf("%s: err = %v, want ERR_RATE_LIMITED", name, ae)
		}
	}
	if n := pol.evals.Load(); n != 0 {
		t.Errorf("policy evaluated %d times, want 0 for refused calls", n)
	}

	// The admin API asks about another subject: no rate limit of that
	// subject applies.
	if _, err := svc.Explain(ctx, queryservice.ExplainInput{User: user, Table: ref, Origin: queryservice.OriginAdmin}); err != nil {
		t.Errorf("admin Explain: %v", err)
	}
}

// Sample runs admission before it parses the statement.
func TestSample_AdmissionBeforeParse(t *testing.T) {
	p := &countingParser{}
	svc := metadataService(&factsPolicy{}, p, &denyingRateLimiter{}, 1, 1)
	_, err := svc.Sample(context.Background(), queryservice.SampleRequest{
		Table: "c0.main.t000", Rows: 5, User: &identity.UserCtx{Subject: "alice"},
	})
	if ae := pkgerr.FromError(err); ae == nil || ae.Code != pkgerr.CodeRateLimited {
		t.Fatalf("err = %v, want ERR_RATE_LIMITED", err)
	}
	if n := p.calls.Load(); n != 0 {
		t.Fatalf("parser ran %d times before admission", n)
	}
}

// The visibility check sees the request facts, and the catalog list stops
// at the first visible table of each catalog.
func TestMetadata_FactsAndCatalogScan(t *testing.T) {
	pol := &factsPolicy{}
	svc := metadataService(pol, &fakeParser{}, nil, 3, 50)
	ctx := context.Background()
	facts := policy.NewRequestFacts(http.Header{"X-Team": {"a"}}, "10.0.0.1")

	got, err := svc.ListCatalogs(ctx, fixedCatalogs{{Name: "c0"}, {Name: "c1"}, {Name: "c2"}}, nil, facts)
	if err != nil || len(got) != 3 {
		t.Fatalf("ListCatalogs = %v, %v; want 3 catalogs", got, err)
	}
	if n := pol.evals.Load(); n != 3 {
		t.Errorf("ListCatalogs evaluated %d times, want 3 (one per catalog)", n)
	}
	if _, err := svc.DescribeTable(ctx, parser.TableRef{Catalog: "c0", Schema: "main", Table: "t000"}, nil, facts); err != nil {
		t.Fatalf("DescribeTable: %v", err)
	}
	if _, _, err := svc.ListTables(ctx, "c0", "", nil, facts, queryservice.Page{Limit: 2}); err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	if _, err := svc.Explain(ctx, queryservice.ExplainInput{Facts: facts, Table: parser.TableRef{Catalog: "c0", Table: "t000"}}); err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if pol.withFacts.Load() != pol.evals.Load()+1 {
		t.Errorf("%d of %d policy calls had the request facts, want all", pol.withFacts.Load(), pol.evals.Load()+1)
	}
}
