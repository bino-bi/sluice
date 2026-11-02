// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/approval"
	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/policycache"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

type snapPolicy struct{}

func (snapPolicy) Evaluate(context.Context, policy.Input) (*policy.Decision, error) {
	return &policy.Decision{Outcome: policy.OutcomeAllow}, nil
}

func (snapPolicy) Explain(context.Context, policy.Input) (*apitypes.ExplainResult, error) {
	return &apitypes.ExplainResult{}, nil
}

func (snapPolicy) SnapshotInfo() (int64, string, []string, bool) { return 1, "d", nil, false }

type nopRewriter struct{}

func (nopRewriter) Rewrite(context.Context, rewriter.RewriteRequest) (*rewriter.RewriteResult, error) {
	return nil, errors.New("not used")
}

type nopExecutor struct{}

func (nopExecutor) Execute(context.Context, executor.Request) (*executor.Response, error) {
	return nil, errors.New("not used")
}

type recordingAudit struct {
	mu      sync.Mutex
	records []*audit.Record
}

func (a *recordingAudit) Enqueue(_ context.Context, r *audit.Record) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.records = append(a.records, r)
	return nil
}

// pendingBroker opens a ticket and keeps it pending.
type pendingBroker struct{}

func (pendingBroker) Require(context.Context, approval.RequireInput) (approval.Ticket, bool, error) {
	return approval.Ticket{ID: "appr-1", State: approval.StatePending}, true, nil
}

func (pendingBroker) Wait(context.Context, string, time.Duration) (approval.State, error) {
	return approval.StatePending, nil
}

func (pendingBroker) ConsumeGrant(string, string) (string, bool) { return "", false }

func (pendingBroker) Get(string) (approval.View, bool) { return approval.View{}, false }

func newInternalService(limits Limits, mod func(*Options)) *Service {
	opts := Options{
		Parser:   pgquery.New(parser.Options{}),
		Policy:   snapPolicy{},
		Rewriter: nopRewriter{},
		Executor: nopExecutor{},
		Audit:    &recordingAudit{},
		Limits:   limits,
	}
	if mod != nil {
		mod(&opts)
	}
	return New(opts)
}

func seedPtr(v int64) *int64 { return &v }

func TestBuildSample_Validation(t *testing.T) {
	svc := newInternalService(Limits{}, nil)
	const tbl = "shop.main.customers"
	cases := []struct {
		name string
		req  SampleRequest
		msg  string // substring of the message; empty means accepted
	}{
		{"no table", SampleRequest{Rows: 1}, "table is required"},
		{"two-part table", SampleRequest{Table: "main.customers", Rows: 1}, "catalog.schema.table"},
		{"empty table part", SampleRequest{Table: "shop..customers", Rows: 1}, "empty name"},
		{"control char", SampleRequest{Table: "shop.main.cu\x01st", Rows: 1}, "control character"},
		{"long name", SampleRequest{Table: "shop.main." + strings.Repeat("a", 64), Rows: 1}, "longer than 63"},
		{"invalid utf8", SampleRequest{Table: "shop.main.\xff", Rows: 1}, "UTF-8"},
		{"reserved column", SampleRequest{Table: tbl, Columns: []string{"sluice_x"}, Rows: 1}, "reserved"},
		{"reserved column upper", SampleRequest{Table: tbl, Columns: []string{"SLUICE_h"}, Rows: 1}, "reserved"},
		{"duplicate column", SampleRequest{Table: tbl, Columns: []string{"id", "ID"}, Rows: 1}, "duplicate"},
		{"rows and rate", SampleRequest{Table: tbl, Rows: 1, Rate: 0.5}, "not both"},
		{"neither", SampleRequest{Table: tbl}, "one of rows or rate"},
		{"rate 0 reads as absent", SampleRequest{Table: tbl, Rate: 0}, "one of rows or rate"},
		{"negative rows", SampleRequest{Table: tbl, Rows: -1}, "rows must be at least 1"},
		{"denormal rate", SampleRequest{Table: tbl, Rate: 5e-324}, "rate must be in"},
		{"rate below min", SampleRequest{Table: tbl, Rate: 9e-7}, "rate must be in"},
		{"rate at min", SampleRequest{Table: tbl, Rate: 1e-6}, ""},
		{"rate above 1", SampleRequest{Table: tbl, Rate: 1.5}, "rate must be in"},
		{"rate NaN", SampleRequest{Table: tbl, Rate: math.NaN()}, "rate must be in"},
		{"reservoir with rate", SampleRequest{Table: tbl, Rate: 0.5, Method: apitypes.SampleMethodReservoir}, "needs rows"},
		{"bernoulli with rows", SampleRequest{Table: tbl, Rows: 5, Method: apitypes.SampleMethodBernoulli}, "needs rate"},
		{"system with rows", SampleRequest{Table: tbl, Rows: 5, Method: apitypes.SampleMethodSystem}, "needs rate"},
		{"stratified without stratify_by", SampleRequest{Table: tbl, Rows: 5, Method: apitypes.SampleMethodStratified}, "stratify_by is required"},
		{"stratify_by with reservoir", SampleRequest{Table: tbl, Rows: 5, StratifyBy: []string{"kind"}}, "needs method stratified"},
		{"stratify_by not in columns", SampleRequest{Table: tbl, Rows: 5, Method: apitypes.SampleMethodStratified, Columns: []string{"id"}, StratifyBy: []string{"kind"}}, "not in columns"},
		{"stratify_by duplicate", SampleRequest{Table: tbl, Rows: 5, Method: apitypes.SampleMethodStratified, StratifyBy: []string{"kind", "Kind"}}, "duplicate"},
		{"stratified ok", SampleRequest{Table: tbl, Rate: 0.1, Method: apitypes.SampleMethodStratified, Columns: []string{"id", "Kind"}, StratifyBy: []string{"kind"}}, ""},
		{"seed -1", SampleRequest{Table: tbl, Rows: 1, Seed: seedPtr(-1)}, "seed must be in"},
		{"seed too large", SampleRequest{Table: tbl, Rows: 1, Seed: seedPtr(2147483648)}, "seed must be in"},
		{"seed max", SampleRequest{Table: tbl, Rows: 1, Seed: seedPtr(2147483647)}, ""},
		{"seed 0", SampleRequest{Table: tbl, Rows: 1, Seed: seedPtr(0)}, ""},
		{"unknown method", SampleRequest{Table: tbl, Rows: 1, Method: "random"}, "method must be"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := svc.buildSample(tc.req)
			if tc.msg == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			ae := pkgerr.FromError(err)
			if ae == nil || ae.Code != pkgerr.CodeSyntax {
				t.Fatalf("err = %v, want %s", err, pkgerr.CodeSyntax)
			}
			if !strings.Contains(ae.Message, tc.msg) {
				t.Errorf("message = %q, want it to contain %q", ae.Message, tc.msg)
			}
		})
	}
}

func TestBuildSample_DefaultMethod(t *testing.T) {
	svc := newInternalService(Limits{}, nil)
	_, sp, _, err := svc.buildSample(SampleRequest{Table: "a.b.c", Rows: 3})
	if err != nil || sp.Method != apitypes.SampleMethodReservoir {
		t.Errorf("rows: method = %v, err = %v, want reservoir", sp, err)
	}
	_, sp, _, err = svc.buildSample(SampleRequest{Table: "a.b.c", Rate: 0.5})
	if err != nil || sp.Method != apitypes.SampleMethodBernoulli {
		t.Errorf("rate: method = %v, err = %v, want bernoulli", sp, err)
	}
}

func TestBuildSample_SQL(t *testing.T) {
	svc := newInternalService(Limits{}, nil)
	cases := []struct {
		req  SampleRequest
		want string
	}{
		{
			SampleRequest{Table: "shop.main.customers", Columns: []string{"id", `we"ird`}, Rows: 3},
			`SELECT "id", "we""ird" FROM "shop"."main"."customers"`,
		},
		{
			SampleRequest{Table: "Shop.Main.Customers", Rows: 3},
			`SELECT * FROM "Shop"."Main"."Customers"`,
		},
		{
			SampleRequest{Table: "shop.main.customers", Rows: 3, Where: "  id > 1 "},
			`SELECT * FROM "shop"."main"."customers" WHERE (id > 1)`,
		},
		{
			SampleRequest{Table: "shop.main.customers", Rows: 3, Where: "   "},
			`SELECT * FROM "shop"."main"."customers"`,
		},
	}
	for _, tc := range cases {
		sql, _, _, err := svc.buildSample(tc.req)
		if err != nil {
			t.Fatalf("buildSample: %v", err)
		}
		if sql != tc.want {
			t.Errorf("SQL = %s, want %s", sql, tc.want)
		}
	}
}

func TestBuildSample_RowsCap(t *testing.T) {
	cases := []struct {
		name   string
		limits Limits
		want   int64
	}{
		{"sample cap smallest", Limits{SampleMaxRows: 5, DefaultMaxRows: 100, MaxRowsCeiling: 100}, 5},
		{"default rows smallest", Limits{SampleMaxRows: 50, DefaultMaxRows: 7, MaxRowsCeiling: 100}, 7},
		{"ceiling smallest", Limits{SampleMaxRows: 50, DefaultMaxRows: 100, MaxRowsCeiling: 10}, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newInternalService(tc.limits, nil)
			_, sp, maxRows, err := svc.buildSample(SampleRequest{Table: "a.b.c", Rows: 1000})
			if err != nil {
				t.Fatalf("buildSample: %v", err)
			}
			if sp.Rows != tc.want || maxRows != tc.want+1 {
				t.Errorf("rows = %d, maxRows = %d, want %d and %d", sp.Rows, maxRows, tc.want, tc.want+1)
			}
		})
	}
	svc := newInternalService(Limits{}, nil)
	if svc.opts.Limits.SampleMaxRows != defaultSampleMaxRows {
		t.Errorf("SampleMaxRows fallback = %d", svc.opts.Limits.SampleMaxRows)
	}
	_, sp, maxRows, err := svc.buildSample(SampleRequest{Table: "a.b.c", Rate: 0.5})
	if err != nil {
		t.Fatalf("buildSample: %v", err)
	}
	if maxRows != 0 || sp.Rows != 0 {
		t.Errorf("rate mode: rows = %d, maxRows = %d, want 0 and 0", sp.Rows, maxRows)
	}
}

func TestBuildSample_RandomSeed(t *testing.T) {
	svc := newInternalService(Limits{SampleMinGroupRows: 3}, nil)
	for range 20 {
		_, sp, _, err := svc.buildSample(SampleRequest{Table: "a.b.c", Rows: 1})
		if err != nil {
			t.Fatalf("buildSample: %v", err)
		}
		if sp.Seed < 0 || sp.Seed > rewriter.MaxSampleSeed {
			t.Fatalf("seed %d out of range", sp.Seed)
		}
		if sp.MinGroupRows != 3 {
			t.Fatalf("MinGroupRows = %d, want 3", sp.MinGroupRows)
		}
	}
}

func TestCacheKey_SkipsSample(t *testing.T) {
	svc := newInternalService(Limits{}, func(o *Options) { o.Cache = policycache.New(16, time.Minute) })
	req := QueryRequest{SQL: "SELECT * FROM a.b.c"}
	if _, ok := svc.cacheKey(req, nil); !ok {
		t.Fatal("plain query not cacheable, the test proves nothing")
	}
	req.Sample = &rewriter.SampleSpec{Method: apitypes.SampleMethodReservoir, Rows: 3}
	if _, ok := svc.cacheKey(req, nil); ok {
		t.Error("sample is cacheable, want not")
	}
}

func TestApprovalHash(t *testing.T) {
	const sql = "SELECT * FROM a.b.c"
	plain := QueryRequest{SQL: sql}
	if approvalHash(plain) != sqlHashHex(sql) {
		t.Error("plain hash differs from sqlHashHex")
	}
	sample := func(sp rewriter.SampleSpec) string {
		return approvalHash(QueryRequest{SQL: sql, Sample: &sp})
	}
	res := rewriter.SampleSpec{Method: apitypes.SampleMethodReservoir, Rows: 1, Seed: 1}
	if sample(res) == approvalHash(plain) {
		t.Error("sample hash equals plain hash")
	}
	// A plain query can carry a NUL and the spec JSON after the SQL.
	forged := QueryRequest{SQL: sql + "\x00" + `{"Method":"reservoir","Rows":1,"Rate":0,"StratifyBy":null}`}
	if sample(res) == approvalHash(forged) {
		t.Error("sample hash equals the hash of a NUL-suffixed plain query")
	}
	other := res
	other.Seed = 2
	if sample(res) != sample(other) {
		t.Error("hash depends on the seed")
	}
	other.Rows = 11
	if sample(res) == sample(other) {
		t.Error("rows 1 and rows 11 give the same hash")
	}
	a := rewriter.SampleSpec{Method: apitypes.SampleMethodStratified, Rows: 1, StratifyBy: []string{"a", "b"}}
	b := rewriter.SampleSpec{Method: apitypes.SampleMethodStratified, Rows: 1, StratifyBy: []string{"ab"}}
	if sample(a) == sample(b) {
		t.Error(`stratify_by ["a","b"] and ["ab"] give the same hash`)
	}
}

func TestAwaitApproval_ExtrasNotShared(t *testing.T) {
	aud := &recordingAudit{}
	svc := newInternalService(Limits{}, func(o *Options) {
		o.Audit = aud
		o.Approvals = pendingBroker{}
		o.ApprovalSyncWait = time.Millisecond
	})
	sp := &rewriter.SampleSpec{Method: apitypes.SampleMethodReservoir, Rows: 3}
	req := QueryRequest{SQL: "SELECT * FROM a.b.c", Sample: sp}
	rec := svc.buildAuditBase(req, "q1", nil)
	dec := &policy.Decision{Outcome: policy.OutcomeAllow, Approval: &policy.ApprovalRequirement{}}

	_, err := svc.awaitApproval(context.Background(), req, dec, rec, "q1", time.Now(), approvalHash(req), "k")
	if ae := pkgerr.FromError(err); ae == nil || ae.Code != pkgerr.CodeApprovalPending {
		t.Fatalf("err = %v, want %s", err, pkgerr.CodeApprovalPending)
	}
	if len(aud.records) != 2 {
		t.Fatalf("records = %d, want 2", len(aud.records))
	}
	requested, pending := aud.records[0], aud.records[1]
	if requested.EventType != audit.EventApprovalRequested || requested.Extras["approval_id"] != "appr-1" {
		t.Errorf("requested record = %+v", requested)
	}
	if _, ok := pending.Extras["approval_id"]; ok {
		t.Errorf("pending record carries approval_id: %v", pending.Extras)
	}
	if pending.Extras["action"] != "sample" || requested.Extras["action"] != "sample" {
		t.Errorf("action missing: %v / %v", requested.Extras, pending.Extras)
	}
}

func TestSampleReason(t *testing.T) {
	cases := map[string]rewriter.SampleSpec{
		"sample: method=reservoir rows=5":                  {Method: apitypes.SampleMethodReservoir, Rows: 5},
		"sample: method=bernoulli rate=0.25":               {Method: apitypes.SampleMethodBernoulli, Rate: 0.25},
		"sample: method=stratified rows=9 stratify_by=a,b": {Method: apitypes.SampleMethodStratified, Rows: 9, StratifyBy: []string{"a", "b"}},
	}
	for want, sp := range cases {
		if got := sampleReason(&sp); got != want {
			t.Errorf("sampleReason = %q, want %q", got, want)
		}
	}
}
