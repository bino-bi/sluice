// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/internal/schema"
	"github.com/bino-bi/sluice/internal/telemetry"
	pkgapi "github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// ---- fake parser ----

type fakeAST struct {
	shape       parser.QueryShape
	tables      []parser.TableRef
	fingerprint string
	stmt        parser.StmtKind
	source      string
}

func (a *fakeAST) Raw() any                   { return nil }
func (a *fakeAST) Fingerprint() string        { return a.fingerprint }
func (a *fakeAST) Tables() []parser.TableRef  { return a.tables }
func (a *fakeAST) Catalogs() []string         { return []string{"pg"} }
func (a *fakeAST) Shape() parser.QueryShape   { return a.shape }
func (a *fakeAST) Clone() parser.AST          { c := *a; return &c }
func (a *fakeAST) Statement() parser.StmtKind { return a.stmt }
func (a *fakeAST) Source() string             { return a.source }

type fakeParser struct {
	ast *fakeAST
	err error
}

func (p *fakeParser) Parse(_ context.Context, sql string) (parser.AST, error) {
	if p.err != nil {
		return nil, p.err
	}
	if p.ast == nil {
		return &fakeAST{
			tables:      []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "orders"}},
			stmt:        parser.StmtSelect,
			fingerprint: "fp-in",
			source:      sql,
		}, nil
	}
	cp := *p.ast
	cp.source = sql
	return &cp, nil
}
func (p *fakeParser) Deparse(_ context.Context, _ parser.AST) (string, error) { return "", nil }
func (p *fakeParser) Fingerprint(_ string) (string, error)                    { return "fp-in", nil }
func (p *fakeParser) Name() string                                            { return "fake" }

// ---- fake policy engine ----

type fakePolicy struct {
	decision *policy.Decision
	err      error
}

func (p *fakePolicy) Evaluate(_ context.Context, _ policy.Input) (*policy.Decision, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.decision, nil
}
func (p *fakePolicy) Explain(_ context.Context, _ policy.Input) (*pkgapi.ExplainResult, error) {
	return &pkgapi.ExplainResult{Effective: pkgapi.EffectiveDecision{Decision: "allow"}}, p.err
}

// ---- fake rewriter ----

type fakeRewriter struct {
	result *rewriter.RewriteResult
	err    error
}

func (r *fakeRewriter) Rewrite(_ context.Context, req rewriter.RewriteRequest) (*rewriter.RewriteResult, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.result != nil {
		return r.result, nil
	}
	return &rewriter.RewriteResult{
		SQL:         req.Raw,
		Fingerprint: "fp-out",
	}, nil
}

// ---- fake executor ----

type fakeExecutor struct {
	columns   []executor.ColumnInfo
	rows      [][]any
	err       error
	lastReq   executor.Request
	execCount int
	// truncateAfter > 0 stops iteration after that many rows and flips
	// Response.Truncated mid-stream, like the real sqlRowIterator.
	truncateAfter int
}

func (e *fakeExecutor) Execute(_ context.Context, req executor.Request) (*executor.Response, error) {
	e.lastReq = req
	e.execCount++
	if e.err != nil {
		return nil, e.err
	}
	rowCount := new(int64)
	iter := &fakeIter{rows: e.rows, rowCount: rowCount, truncateAfter: e.truncateAfter}
	resp := &executor.Response{Columns: e.columns, Rows: iter, RowCount: rowCount}
	iter.resp = resp
	return resp, nil
}

type fakeIter struct {
	rows          [][]any
	rowCount      *int64
	idx           int
	closed        bool
	err           error
	truncateAfter int
	resp          *executor.Response
}

func (it *fakeIter) Next() bool {
	if it.closed || it.idx >= len(it.rows) {
		return false
	}
	if it.truncateAfter > 0 && it.idx >= it.truncateAfter {
		it.resp.Truncated = true
		return false
	}
	*it.rowCount++
	return true
}
func (it *fakeIter) Scan(dest ...any) error {
	if len(dest) != len(it.rows[it.idx]) {
		return errors.New("fake: scan mismatch")
	}
	for i, v := range it.rows[it.idx] {
		if d, ok := dest[i].(*any); ok {
			*d = v
		}
	}
	it.idx++
	return nil
}
func (it *fakeIter) Err() error { return it.err }
func (it *fakeIter) Close() error {
	if it.closed {
		return nil
	}
	it.closed = true
	return nil
}

// ---- fake audit dispatcher ----

type fakeAudit struct {
	mu      sync.Mutex
	records []*audit.Record
	err     error
}

func (a *fakeAudit) Enqueue(_ context.Context, r *audit.Record) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	a.records = append(a.records, r)
	return nil
}
func (a *fakeAudit) all() []*audit.Record {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]*audit.Record(nil), a.records...)
}

// ---- helpers ----

func buildService(t *testing.T, p *fakeParser, pol *fakePolicy, rw *fakeRewriter, ex *fakeExecutor, a *fakeAudit) *queryservice.Service {
	t.Helper()
	return queryservice.New(queryservice.Options{
		Parser:   p,
		Policy:   pol,
		Rewriter: rw,
		Executor: ex,
		Audit:    a,
		Clock:    func() time.Time { return time.Unix(1713600000, 0) },
	})
}

// ---- tests ----

func TestExecute_HappyPath(t *testing.T) {
	a := &fakeAudit{}
	svc := buildService(t,
		&fakeParser{},
		&fakePolicy{decision: &policy.Decision{
			Outcome: policy.OutcomeAllow,
			Applied: []pkgapi.AppliedPolicy{{Kind: pkgapi.KindSQLAccessPolicy, Name: "p1"}},
		}},
		&fakeRewriter{},
		&fakeExecutor{
			columns: []executor.ColumnInfo{{Name: "id"}, {Name: "name"}},
			rows:    [][]any{{int64(1), "alice"}, {int64(2), "bob"}},
		},
		a,
	)
	res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
		SQL:    "SELECT id, name FROM pg.public.orders",
		Origin: queryservice.OriginREST,
		User:   &identity.UserCtx{Subject: "alice", RemoteAddr: "10.1.2.3"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.QueryID == "" {
		t.Fatalf("query id missing")
	}
	// Drain & close to trigger audit emission.
	for res.Rows.Next() {
		var id, name any
		if err := res.Rows.Scan(&id, &name); err != nil {
			t.Fatalf("scan: %v", err)
		}
	}
	if err := res.Rows.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	recs := a.all()
	// Fail-closed model: an access record (emitted before rows are served)
	// plus a best-effort completion record carrying the final RowCount.
	if len(recs) != 2 {
		t.Fatalf("expected 2 audit records (access + result), got %d", len(recs))
	}
	access := recs[0]
	if access.EventType != audit.EventQuery {
		t.Fatalf("record[0] event_type = %s, want query", access.EventType)
	}
	if access.Decision != audit.DecisionAllow {
		t.Fatalf("access decision = %s, want allow", access.Decision)
	}
	if access.SQLFingerprint == "" {
		t.Fatalf("sql_fingerprint empty on access record")
	}
	result := recs[1]
	if result.EventType != audit.EventQueryResult {
		t.Fatalf("record[1] event_type = %s, want query-result", result.EventType)
	}
	if result.RowCount != 2 {
		t.Fatalf("result row_count = %d, want 2", result.RowCount)
	}
	// The result record names who got the rows, like the access record.
	if result.Subject.ID != access.Subject.ID || result.Subject.ID == "" || result.Origin != access.Origin || result.RemoteIP != access.RemoteIP {
		t.Fatalf("result record: subject %+v origin %q remote_ip %q, want those of the access record (%+v, %q, %q)",
			result.Subject, result.Origin, result.RemoteIP, access.Subject, access.Origin, access.RemoteIP)
	}
}

func TestExecute_ClampsMaxRowsAndTimeout(t *testing.T) {
	a := &fakeAudit{}
	ex := &fakeExecutor{columns: []executor.ColumnInfo{{Name: "id"}}}
	svc := queryservice.New(queryservice.Options{
		Parser:   &fakeParser{},
		Policy:   &fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		Rewriter: &fakeRewriter{},
		Executor: ex,
		Audit:    a,
		Clock:    func() time.Time { return time.Unix(1713600000, 0) },
		Limits: queryservice.Limits{
			DefaultMaxRows: 100,
			MaxRowsCeiling: 10,
			DefaultTimeout: 30 * time.Second,
			MaxTimeout:     5 * time.Second,
		},
	})
	res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
		SQL:     "SELECT id FROM pg.public.orders",
		MaxRows: 2_000_000_000,
		Timeout: time.Hour,
		Origin:  queryservice.OriginMCP,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	_ = res.Rows.Close()
	if ex.lastReq.MaxRows != 10 {
		t.Errorf("MaxRows = %d, want clamped to 10", ex.lastReq.MaxRows)
	}
	if ex.lastReq.Timeout != 5*time.Second {
		t.Errorf("Timeout = %s, want clamped to 5s", ex.lastReq.Timeout)
	}
}

type denyingRateLimiter struct{}

func (denyingRateLimiter) Allow(_, _ string) bool { return false }

// blockingExecutor holds every call until release is closed.
type blockingExecutor struct {
	started chan struct{}
	release chan struct{}
}

func (e *blockingExecutor) Execute(context.Context, executor.Request) (*executor.Response, error) {
	e.started <- struct{}{}
	<-e.release
	return nil, errors.New("released")
}

// A request refused because every concurrency slot is in use leaves an
// audit record, and the client gets a query id for it.
func TestExecute_SlotFullIsAudited(t *testing.T) {
	a := &fakeAudit{}
	ex := &blockingExecutor{started: make(chan struct{}), release: make(chan struct{})}
	svc := queryservice.New(queryservice.Options{
		Parser:   &fakeParser{},
		Policy:   &fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		Rewriter: &fakeRewriter{},
		Executor: ex,
		Audit:    a,
		Limits:   queryservice.Limits{MaxConcurrent: 1},
	})
	req := queryservice.QueryRequest{
		SQL:    "SELECT id FROM pg.public.orders",
		Origin: queryservice.OriginREST,
		User:   &identity.UserCtx{Subject: "alice", RemoteAddr: "10.1.2.3"},
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = svc.Execute(context.Background(), req)
	}()
	<-ex.started // the first request holds the only slot

	_, err := svc.Execute(context.Background(), req)
	ae := pkgerr.FromError(err)
	if err == nil || ae.Code != pkgerr.CodeRateLimited || ae.QueryID == "" {
		t.Fatalf("err = %v (query id %q), want ERR_RATE_LIMITED with a query id", err, ae.QueryID)
	}
	var refused []*audit.Record
	for _, rec := range a.all() {
		if rec.ErrorCode == string(pkgerr.CodeRateLimited) {
			refused = append(refused, rec)
		}
	}
	if len(refused) != 1 || refused[0].QueryID != ae.QueryID || refused[0].Subject.ID != "alice" || refused[0].Message != "service saturated" {
		t.Fatalf("records of the refused request = %+v, want one with its query id, the subject and the message", refused)
	}
	close(ex.release)
	<-done
}

func TestExecute_RateLimitedSubject(t *testing.T) {
	a := &fakeAudit{}
	svc := queryservice.New(queryservice.Options{
		Parser:      &fakeParser{},
		Policy:      &fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		Rewriter:    &fakeRewriter{},
		Executor:    &fakeExecutor{},
		Audit:       a,
		Clock:       func() time.Time { return time.Unix(1713600000, 0) },
		RateLimiter: denyingRateLimiter{},
	})
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{
		SQL:    "SELECT id FROM pg.public.orders",
		Origin: queryservice.OriginMCP,
		User:   &identity.UserCtx{Subject: "alice"},
	})
	if err == nil {
		t.Fatal("expected rate-limit error")
	}
	if ae := pkgerr.FromError(err); ae == nil || ae.Code != pkgerr.CodeRateLimited {
		t.Fatalf("code = %v, want %s", err, pkgerr.CodeRateLimited)
	}
	// A rate-limited request is still audited (decision=error).
	recs := a.all()
	if len(recs) != 1 || recs[0].ErrorCode != string(pkgerr.CodeRateLimited) {
		t.Fatalf("want 1 audit record with rate-limit code, got %+v", recs)
	}
}

func TestExecute_FailClosedWhenAuditUnavailable(t *testing.T) {
	a := &fakeAudit{err: audit.ErrQueueFull}
	ex := &fakeExecutor{columns: []executor.ColumnInfo{{Name: "id"}}, rows: [][]any{{int64(1)}}}
	svc := queryservice.New(queryservice.Options{
		Parser:   &fakeParser{},
		Policy:   &fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		Rewriter: &fakeRewriter{},
		Executor: ex,
		Audit:    a,
		Clock:    func() time.Time { return time.Unix(1713600000, 0) },
		// AuditBestEffort defaults false → fail-closed.
	})
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{
		SQL:    "SELECT id FROM pg.public.orders",
		Origin: queryservice.OriginMCP,
	})
	if err == nil {
		t.Fatal("expected fail-closed error when audit cannot be enqueued")
	}
	if ae := pkgerr.FromError(err); ae == nil || ae.Code != pkgerr.CodeAuditUnavailable {
		t.Fatalf("code = %v, want %s", err, pkgerr.CodeAuditUnavailable)
	}
}

func TestExecute_BestEffortServesWhenAuditUnavailable(t *testing.T) {
	a := &fakeAudit{err: audit.ErrQueueFull}
	ex := &fakeExecutor{columns: []executor.ColumnInfo{{Name: "id"}}, rows: [][]any{{int64(1)}}}
	svc := queryservice.New(queryservice.Options{
		Parser:          &fakeParser{},
		Policy:          &fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		Rewriter:        &fakeRewriter{},
		Executor:        ex,
		Audit:           a,
		Clock:           func() time.Time { return time.Unix(1713600000, 0) },
		AuditBestEffort: true,
	})
	res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
		SQL:    "SELECT id FROM pg.public.orders",
		Origin: queryservice.OriginMCP,
	})
	if err != nil {
		t.Fatalf("best-effort must serve despite audit failure: %v", err)
	}
	_ = res.Rows.Close()
}

func TestExecute_DenyEmitsOnce(t *testing.T) {
	a := &fakeAudit{}
	svc := buildService(t,
		&fakeParser{},
		&fakePolicy{decision: &policy.Decision{
			Outcome:    policy.OutcomeDeny,
			DenyReason: &policy.DenyReason{PolicyName: "deny-all", Message: "blocked"},
		}},
		&fakeRewriter{},
		&fakeExecutor{},
		a,
	)
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{
		SQL:    "SELECT * FROM pg.public.orders",
		Origin: queryservice.OriginREST,
	})
	if err == nil {
		t.Fatalf("expected deny error")
	}
	ae := pkgerr.FromError(err)
	if ae.Code != pkgerr.CodeACLDenied {
		t.Fatalf("code = %s, want %s", ae.Code, pkgerr.CodeACLDenied)
	}
	if ae.Policy != "deny-all" {
		t.Fatalf("policy = %q, want deny-all", ae.Policy)
	}
	recs := a.all()
	if len(recs) != 1 {
		t.Fatalf("deny must emit exactly 1 audit record, got %d", len(recs))
	}
	if recs[0].Decision != audit.DecisionDeny {
		t.Fatalf("decision = %s, want deny", recs[0].Decision)
	}
}

func TestExecute_RejectEmitsOnce(t *testing.T) {
	a := &fakeAudit{}
	svc := buildService(t,
		&fakeParser{},
		&fakePolicy{decision: &policy.Decision{
			Outcome:    policy.OutcomeReject,
			Rejections: []policy.Rejection{{PolicyName: "no-cart", RuleName: "rule-1", Message: "cart blocked"}},
		}},
		&fakeRewriter{},
		&fakeExecutor{},
		a,
	)
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{
		SQL:    "SELECT 1",
		Origin: queryservice.OriginREST,
	})
	if err == nil {
		t.Fatalf("expected reject error")
	}
	ae := pkgerr.FromError(err)
	if ae.Code != pkgerr.CodeACLRejected {
		t.Fatalf("code = %s, want %s", ae.Code, pkgerr.CodeACLRejected)
	}
	if a.all()[0].Decision != audit.DecisionReject {
		t.Fatalf("expected reject decision in audit")
	}
}

func TestExecute_AuditSQLSample(t *testing.T) {
	sql := "SELECT id, name FROM pg.public.orders WHERE id = 42"
	cases := []struct {
		name        string
		sampleBytes int
		want        string
	}{
		{"capped", 16, sql[:16]},
		{"disabled", 0, ""},
		{"larger than sql", 4096, sql},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &fakeAudit{}
			svc := queryservice.New(queryservice.Options{
				Parser:   &fakeParser{},
				Policy:   &fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
				Rewriter: &fakeRewriter{},
				Executor: &fakeExecutor{columns: []executor.ColumnInfo{{Name: "id"}}},
				Audit:    a,
				Clock:    func() time.Time { return time.Unix(1713600000, 0) },
				Limits:   queryservice.Limits{SQLSampleBytes: tc.sampleBytes},
			})
			res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
				SQL:    sql,
				Origin: queryservice.OriginREST,
			})
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			_ = res.Rows.Close()
			recs := a.all()
			if len(recs) == 0 {
				t.Fatalf("no audit records")
			}
			if got := recs[0].SQLSample; got != tc.want {
				t.Fatalf("sql_sample = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExecute_ClientMetaAudited(t *testing.T) {
	a := &fakeAudit{}
	svc := buildService(t,
		&fakeParser{},
		&fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		&fakeRewriter{},
		&fakeExecutor{columns: []executor.ColumnInfo{{Name: "id"}}},
		a,
	)
	res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
		SQL:    "SELECT 1",
		Origin: queryservice.OriginREST,
		Metadata: map[string]string{
			"dashboard": "revenue-daily",
			"user":      "alice@example.com",
			"oversized": strings.Repeat("x", 5000),
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	_ = res.Rows.Close()
	recs := a.all()
	if len(recs) == 0 {
		t.Fatalf("no audit records")
	}
	cm := recs[0].ClientMeta
	if cm["dashboard"] != "revenue-daily" || cm["user"] != "alice@example.com" {
		t.Fatalf("client_meta = %v", cm)
	}
	if len(cm["oversized"]) >= 5000 {
		t.Fatalf("oversized value not capped: %d bytes", len(cm["oversized"]))
	}
}

func TestExecute_AttachErrorMapsToDataSourceUnavailable(t *testing.T) {
	a := &fakeAudit{}
	svc := buildService(t,
		&fakeParser{},
		&fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		&fakeRewriter{},
		&fakeExecutor{err: fmt.Errorf("run: %w", executor.ErrAttach)},
		a,
	)
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{
		SQL:    "SELECT 1",
		Origin: queryservice.OriginREST,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	ae := pkgerr.FromError(err)
	if ae.Code != pkgerr.CodeDataSourceUnavailable {
		t.Fatalf("code = %s, want %s", ae.Code, pkgerr.CodeDataSourceUnavailable)
	}
}

func TestExecute_ConnUnavailableMapsToDataSourceUnavailable(t *testing.T) {
	a := &fakeAudit{}
	svc := buildService(t,
		&fakeParser{},
		&fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		&fakeRewriter{},
		&fakeExecutor{err: fmt.Errorf("run: %w", executor.ErrConnUnavailable)},
		a,
	)
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{
		SQL:    "SELECT 1",
		Origin: queryservice.OriginREST,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	ae := pkgerr.FromError(err)
	if ae.Code != pkgerr.CodeDataSourceUnavailable {
		t.Fatalf("code = %s, want %s", ae.Code, pkgerr.CodeDataSourceUnavailable)
	}
}

func TestExecute_UnknownCatalogMapsToDataSourceUnavailable(t *testing.T) {
	a := &fakeAudit{}
	svc := buildService(t,
		&fakeParser{},
		&fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		&fakeRewriter{err: fmt.Errorf("resolve: %w", schema.ErrUnknownCatalog)},
		&fakeExecutor{},
		a,
	)
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{
		SQL:    "SELECT 1",
		Origin: queryservice.OriginREST,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	ae := pkgerr.FromError(err)
	if ae.Code != pkgerr.CodeDataSourceUnavailable {
		t.Fatalf("code = %s, want %s", ae.Code, pkgerr.CodeDataSourceUnavailable)
	}
}

func TestAuditedRows_TruncatedSyncs(t *testing.T) {
	a := &fakeAudit{}
	svc := buildService(t,
		&fakeParser{},
		&fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		&fakeRewriter{},
		&fakeExecutor{
			columns:       []executor.ColumnInfo{{Name: "id"}},
			rows:          [][]any{{int64(1)}, {int64(2)}, {int64(3)}},
			truncateAfter: 2,
		},
		a,
	)
	res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
		SQL:    "SELECT id FROM pg.public.orders",
		Origin: queryservice.OriginREST,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Truncated {
		t.Fatalf("Truncated must be false before iteration")
	}
	for res.Rows.Next() {
		var id any
		if err := res.Rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
	}
	if !res.Truncated {
		t.Fatalf("Truncated not synced onto QueryResult after stream end")
	}
	if err := res.Rows.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	recs := a.all()
	if len(recs) != 2 {
		t.Fatalf("expected 2 audit records, got %d", len(recs))
	}
	if !recs[1].Truncated {
		t.Fatalf("completion audit record must carry Truncated=true")
	}
}

func TestExecute_CrossCatalogGate(t *testing.T) {
	twoCatalogs := []parser.TableRef{
		{Catalog: "pg", Schema: "public", Table: "orders"},
		{Catalog: "ref", Schema: "public", Table: "regions"},
	}
	oneCatalog := []parser.TableRef{
		{Catalog: "pg", Schema: "public", Table: "orders"},
		{Catalog: "pg", Schema: "public", Table: "customers"},
	}
	// Two-part names carry no catalog; the gate cannot see them (parity
	// with the policy rule size(query.catalogs) > 1).
	twoPart := []parser.TableRef{
		{Schema: "sales", Table: "orders"},
		{Schema: "crm", Table: "contacts"},
	}

	cases := []struct {
		name    string
		disable bool
		tables  []parser.TableRef
		reject  bool
	}{
		{"two catalogs, flag on", true, twoCatalogs, true},
		{"one catalog, flag on", true, oneCatalog, false},
		{"two catalogs, flag off", false, twoCatalogs, false},
		{"two-part names, flag on", true, twoPart, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &fakeAudit{}
			ex := &fakeExecutor{columns: []executor.ColumnInfo{{Name: "id"}}}
			svc := queryservice.New(queryservice.Options{
				Parser:   &fakeParser{ast: &fakeAST{tables: tc.tables, stmt: parser.StmtSelect, fingerprint: "fp-in"}},
				Policy:   &fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
				Rewriter: &fakeRewriter{},
				Executor: ex,
				Audit:    a,
				Clock:    func() time.Time { return time.Unix(1713600000, 0) },
				Limits:   queryservice.Limits{DisableCrossCatalog: tc.disable},
			})
			res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
				SQL:    "SELECT 1",
				Origin: queryservice.OriginREST,
			})
			if !tc.reject {
				if err != nil {
					t.Fatalf("execute: %v", err)
				}
				_ = res.Rows.Close()
				if ex.execCount != 1 {
					t.Fatalf("execCount = %d, want 1", ex.execCount)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected cross-catalog reject")
			}
			ae := pkgerr.FromError(err)
			if ae.Code != pkgerr.CodeACLRejected {
				t.Fatalf("code = %s, want %s", ae.Code, pkgerr.CodeACLRejected)
			}
			if ex.execCount != 0 {
				t.Fatalf("executor must not run on reject, got %d calls", ex.execCount)
			}
			recs := a.all()
			if len(recs) != 1 {
				t.Fatalf("reject must emit exactly 1 audit record, got %d", len(recs))
			}
			if recs[0].Decision != audit.DecisionReject {
				t.Fatalf("decision = %s, want reject", recs[0].Decision)
			}
			if recs[0].Message == "" {
				t.Fatalf("reject audit record must carry a message")
			}
		})
	}
}

func TestExecute_ParseErrorWithAllow(t *testing.T) {
	a := &fakeAudit{}
	svc := buildService(t,
		&fakeParser{err: parser.ErrSyntax},
		&fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		&fakeRewriter{},
		&fakeExecutor{},
		a,
	)
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{
		SQL:    "I AM NOT SQL",
		Origin: queryservice.OriginREST,
	})
	if err == nil {
		t.Fatalf("expected syntax error")
	}
	ae := pkgerr.FromError(err)
	if ae.Code != pkgerr.CodeSyntax {
		t.Fatalf("code = %s, want %s", ae.Code, pkgerr.CodeSyntax)
	}
	if a.all()[0].Decision != audit.DecisionError {
		t.Fatalf("expected error decision")
	}
}

func TestExecute_RewriteErrorEmitsOnce(t *testing.T) {
	a := &fakeAudit{}
	svc := buildService(t,
		&fakeParser{},
		&fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		&fakeRewriter{err: rewriter.ErrUnsupportedSyntax},
		&fakeExecutor{},
		a,
	)
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT 1"})
	if err == nil {
		t.Fatalf("expected rewrite error")
	}
	ae := pkgerr.FromError(err)
	if ae.Code != pkgerr.CodeUnsupportedSyntax {
		t.Fatalf("code = %s, want unsupported", ae.Code)
	}
	if len(a.all()) != 1 {
		t.Fatalf("expected 1 audit record on rewrite error, got %d", len(a.all()))
	}
}

// A policy engine that cannot decide (a ReBAC or OPA backend is down) is
// an outage: 503 with its own code, not a deny and not ERR_INTERNAL. The
// audit record carries the code the client gets, and the cause is logged.
func TestExecute_PolicyErrorCodes(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		code   pkgerr.Code
		logged bool
	}{
		{"backend down", errors.New("openfga: dial tcp: connection refused"), pkgerr.CodePolicyUnavailable, true},
		{"timeout", fmt.Errorf("openfga check: %w", context.DeadlineExceeded), pkgerr.CodeTimeout, false},
		{"canceled", context.Canceled, pkgerr.CodeCanceled, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var log bytes.Buffer
			a := &fakeAudit{}
			ex := &fakeExecutor{}
			svc := queryservice.New(queryservice.Options{
				Parser:   &fakeParser{},
				Policy:   &fakePolicy{err: tc.err},
				Rewriter: &fakeRewriter{},
				Executor: ex,
				Audit:    a,
				Logger:   telemetry.NewLogger(&log, telemetry.LoggingConfig{Format: "json", MaxErrorBytes: 4096}),
			})
			_, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT 1"})
			ae := pkgerr.FromError(err)
			if ae == nil || ae.Code != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
			if strings.Contains(ae.Message, "openfga") {
				t.Errorf("client message %q holds the cause", ae.Message)
			}
			if recs := a.all(); len(recs) != 1 || recs[0].ErrorCode != string(tc.code) || recs[0].Decision != audit.DecisionError {
				t.Fatalf("audit records = %+v, want one error record with %s", recs, tc.code)
			}
			got := log.String()
			if tc.logged != (strings.Contains(got, `"level":"ERROR"`) && strings.Contains(got, "connection refused") && strings.Contains(got, ae.QueryID)) {
				t.Errorf("log = %q, logged want %v", got, tc.logged)
			}
		})
	}
}

// A row filter that needs a value the subject does not have denies the
// query. The audit record says deny and holds the reason, the client gets
// the plain code.
func TestExecute_MissingTemplateValueIsDeny(t *testing.T) {
	a := &fakeAudit{}
	svc := buildService(t,
		&fakeParser{},
		&fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		&fakeRewriter{err: fmt.Errorf("row filter shop.main.orders: %w: claim tenant_id", policy.ErrTemplateVarMissing)},
		&fakeExecutor{},
		a,
	)
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT 1"})
	ae := pkgerr.FromError(err)
	if err == nil || ae.Code != pkgerr.CodeACLDenied {
		t.Fatalf("err = %v, want ACL_DENIED", err)
	}
	if strings.Contains(ae.Message, "tenant_id") {
		t.Errorf("client message %q names the template value", ae.Message)
	}
	recs := a.all()
	if len(recs) != 1 {
		t.Fatalf("audit records = %d, want 1", len(recs))
	}
	// As for a policy deny, the record has no error code.
	if recs[0].Decision != audit.DecisionDeny || recs[0].ErrorCode != "" || !strings.Contains(recs[0].Message, "tenant_id") {
		t.Errorf("record = decision %q code %q message %q, want deny, no code and the reason", recs[0].Decision, recs[0].ErrorCode, recs[0].Message)
	}
}

// The audit record of a failed execution carries the code the client
// gets. A statement DuckDB refused is logged with its cause, because the
// client only gets the code.
func TestExecute_ExecErrorCodesAndLog(t *testing.T) {
	const cause = "Binder Error: no column x"
	cases := []struct {
		name   string
		err    error
		code   pkgerr.Code
		logged bool
	}{
		{"rejected", fmt.Errorf("%w: %w: %w", executor.ErrExecute, executor.ErrRejected, errors.New(cause)), pkgerr.CodeQueryFailed, true},
		// DuckDB quotes values of the statement. The handler cuts the
		// "error" attribute.
		{"rejected, long cause", fmt.Errorf("%w: %w: %w", executor.ErrExecute, executor.ErrRejected, errors.New(cause+strings.Repeat("A", 100000))), pkgerr.CodeQueryFailed, true},
		{"timeout", context.DeadlineExceeded, pkgerr.CodeTimeout, false},
		{"other", errors.New("boom"), pkgerr.CodeInternal, false},
		// The statement ran as the client wrote it, so a parser error of
		// DuckDB is a syntax error of the client.
		{"parser, statement as sent", fmt.Errorf("%w: %w: %w", executor.ErrExecute, executor.ErrSyntax, errors.New("Parser Error: x")), pkgerr.CodeSyntax, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var log bytes.Buffer
			a := &fakeAudit{}
			svc := queryservice.New(queryservice.Options{
				Parser:   &fakeParser{},
				Policy:   &fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
				Rewriter: &fakeRewriter{},
				Executor: &fakeExecutor{err: tc.err},
				Audit:    a,
				Logger:   telemetry.NewLogger(&log, telemetry.LoggingConfig{Format: "json", MaxErrorBytes: 4096}),
			})
			_, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT 1"})
			ae := pkgerr.FromError(err)
			if ae == nil || ae.Code != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
			if recs := a.all(); len(recs) != 1 || recs[0].ErrorCode != string(tc.code) {
				t.Fatalf("audit records = %+v, want one with %s", recs, tc.code)
			}
			got := log.String()
			if !tc.logged && got != "" {
				t.Fatalf("log = %q, want nothing", got)
			}
			if len(got) > 8192 {
				t.Fatalf("log line has %d bytes, want the cause cut", len(got))
			}
			if tc.logged && (!strings.Contains(got, `"level":"WARN"`) || !strings.Contains(got, ae.QueryID) || !strings.Contains(got, cause)) {
				t.Fatalf("log = %q, want a WARN line with the query_id and the cause", got)
			}
		})
	}
}

// A parser error of DuckDB on SQL that Sluice rewrote is a fault of the
// rewrite, not of the client.
func TestExecute_ParserErrorOnRewrittenSQLStaysInternal(t *testing.T) {
	a := &fakeAudit{}
	svc := buildService(t,
		&fakeParser{},
		&fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		&fakeRewriter{result: &rewriter.RewriteResult{SQL: "SELECT 1 +", Changed: true}},
		&fakeExecutor{err: fmt.Errorf("%w: %w: %w", executor.ErrExecute, executor.ErrSyntax, errors.New("Parser Error: x"))},
		a,
	)
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT 1"})
	if ae := pkgerr.FromError(err); err == nil || ae.Code != pkgerr.CodeInternal {
		t.Fatalf("err = %v, want ERR_INTERNAL", err)
	}
}

// DuckDB reports a source that went away with an error that says nothing
// about the cause (a removed SQLite file gives "Invalid Error"). The last
// health test of the data sources of the query decides between
// ERR_INTERNAL and ERR_DATASOURCE_UNAVAILABLE.
func TestExecute_ExecErrorChecksTheDataSourceHealth(t *testing.T) {
	for name, tc := range map[string]struct {
		down bool
		want pkgerr.Code
	}{
		"source down": {true, pkgerr.CodeDataSourceUnavailable},
		"source fine": {false, pkgerr.CodeInternal},
	} {
		t.Run(name, func(t *testing.T) {
			a := &fakeAudit{}
			var asked []string
			svc := queryservice.New(queryservice.Options{
				Parser:   &fakeParser{},
				Policy:   &fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
				Rewriter: &fakeRewriter{},
				Executor: &fakeExecutor{err: fmt.Errorf("%w: %w", executor.ErrExecute, errors.New("Invalid Error: unable to open database file"))},
				Audit:    a,
				SourceDown: func(catalog string) bool {
					asked = append(asked, catalog)
					return tc.down
				},
			})
			_, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT 1"})
			if ae := pkgerr.FromError(err); err == nil || ae.Code != tc.want {
				t.Fatalf("err = %v, want %s", err, tc.want)
			}
			if fmt.Sprint(asked) != "[pg]" {
				t.Errorf("asked for %v, want the catalog of the query", asked)
			}
			if recs := a.all(); len(recs) != 1 || recs[0].ErrorCode != string(tc.want) {
				t.Errorf("audit records = %+v, want one with %s", recs, tc.want)
			}
		})
	}
}

func TestExecute_ExecErrorEmitsOnce(t *testing.T) {
	a := &fakeAudit{}
	svc := buildService(t,
		&fakeParser{},
		&fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		&fakeRewriter{},
		&fakeExecutor{err: errors.New("boom")},
		a,
	)
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT 1"})
	if err == nil {
		t.Fatalf("expected exec error")
	}
	recs := a.all()
	if len(recs) != 1 {
		t.Fatalf("expected 1 audit record, got %d", len(recs))
	}
	if recs[0].Decision != audit.DecisionError {
		t.Fatalf("decision = %s, want error", recs[0].Decision)
	}
}

func TestExecute_PayloadTooLarge(t *testing.T) {
	a := &fakeAudit{}
	svc := queryservice.New(queryservice.Options{
		Parser:   &fakeParser{},
		Policy:   &fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		Rewriter: &fakeRewriter{},
		Executor: &fakeExecutor{},
		Audit:    a,
		Limits:   queryservice.Limits{MaxSQLBytes: 10},
	})
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "this payload is way over 10 bytes"})
	if err == nil {
		t.Fatalf("expected payload-too-large")
	}
	ae := pkgerr.FromError(err)
	if ae.Code != pkgerr.CodePayloadTooLarge {
		t.Fatalf("code = %s", ae.Code)
	}
}
