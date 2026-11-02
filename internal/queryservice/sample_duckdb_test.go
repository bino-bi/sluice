// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice_test

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/approval"
	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/policycache"
	"github.com/bino-bi/sluice/internal/queryservice"
	pkgapi "github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// sampleSeed adds 1,000 customers (100 of tenant acme) and an events
// table with a group column: 600 a, 390 b and 10 rare rows of acme, and
// 500 rows of widget.
const sampleSeed = `
INSERT INTO customers
SELECT 1000 + i, 'c' || i, 'c' || i || '@x.test', CASE WHEN i <= 100 THEN 'acme' ELSE 'widget' END, '2026-01-01T00:00:00Z'
FROM range(1, 1001) t(i);
CREATE TABLE events (id INTEGER, tenant_id TEXT, kind TEXT);
INSERT INTO events
SELECT i, 'acme', CASE WHEN i <= 600 THEN 'a' WHEN i <= 990 THEN 'b' ELSE 'rare' END
FROM range(1, 1001) t(i);
INSERT INTO events SELECT 1000 + i, 'widget', 'a' FROM range(1, 501) t(i);
`

// acmeCustomers is the number of customers of tenant acme after sampleSeed.
const acmeCustomers = 102

const sampleEventsPolicies = `
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: allow-events
  priority: 100
spec:
  effect: allow
  match:
    any:
      - subjects:
          groups: ["analytics"]
        resources: { catalogs: ["shop"], schemas: ["main"], tables: ["events"] }
---
apiVersion: sluice.bino.bi/v1alpha1
kind: RowFilterPolicy
metadata:
  name: filter-events
  priority: 80
spec:
  match:
    any:
      - resources: { catalogs: ["shop"], schemas: ["main"], tables: ["events"] }
  combine: restrictive
  filter:
    predicate:
      column: tenant_id
      op: Equals
      value: "{{ subject.tenantId }}"
`

const jitterTotalPolicy = `
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata:
  name: jitter-total
  priority: 50
spec:
  match:
    any:
      - resources: { catalogs: ["shop"], schemas: ["main"], tables: ["orders"], columns: ["total_cents"] }
  mask:
    type: jitter
    args: { range: 0.5, seed: "s1" }
`

func newSampleService(t *testing.T, mod func(*queryservice.Options), extraPolicies ...string) (*queryservice.Service, *countingExecutor, *fakeAudit) {
	t.Helper()
	return newSeededHelloSluiceServiceOpts(t, nil, sampleSeed, mod, append([]string{sampleEventsPolicies}, extraPolicies...)...)
}

// runSample runs req as the hello user over REST and drains the rows.
func runSample(t *testing.T, svc *queryservice.Service, req queryservice.SampleRequest) ([][]any, *queryservice.QueryResult, error) {
	t.Helper()
	req.User = helloUser()
	req.Origin = queryservice.OriginREST
	res, err := svc.Sample(context.Background(), req)
	if err != nil {
		return nil, nil, err
	}
	return drainRows(t, res), res, nil
}

func mustSample(t *testing.T, svc *queryservice.Service, req queryservice.SampleRequest) ([][]any, *queryservice.QueryResult) {
	t.Helper()
	rows, res, err := runSample(t, svc, req)
	if err != nil {
		t.Fatalf("Sample(%+v): %v", req, err)
	}
	return rows, res
}

func colIndex(t *testing.T, res *queryservice.QueryResult, name string) int {
	t.Helper()
	for i, c := range res.Columns {
		if c.Name == name {
			return i
		}
	}
	t.Fatalf("column %s not in %v", name, res.Columns)
	return -1
}

func wantCode(t *testing.T, err error, code pkgerr.Code) {
	t.Helper()
	if ae := pkgerr.FromError(err); ae == nil || ae.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
}

func seed(v int64) *int64 { return &v }

// rowKeys renders rows as sorted strings, for an order-free compare.
func rowKeys(rows [][]any) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = fmt.Sprint(r...)
	}
	slices.Sort(out)
	return out
}

func allTenant(t *testing.T, res *queryservice.QueryResult, rows [][]any, tenant string) {
	t.Helper()
	i := colIndex(t, res, "tenant_id")
	for _, r := range rows {
		if r[i] != tenant {
			t.Fatalf("row %v has tenant %v, want %s", r, r[i], tenant)
		}
	}
}

func TestSample_RowFilterBeforeSampling(t *testing.T) {
	svc, _, _ := newSampleService(t, nil)
	rows, res := mustSample(t, svc, queryservice.SampleRequest{Table: "shop.main.customers", Rows: acmeCustomers})
	if len(rows) != acmeCustomers {
		t.Fatalf("rows = %d, want %d", len(rows), acmeCustomers)
	}
	allTenant(t, res, rows, "acme")
}

func TestSample_MixedCaseTable(t *testing.T) {
	svc, _, _ := newSampleService(t, nil)
	rows, res := mustSample(t, svc, queryservice.SampleRequest{Table: "Shop.Main.Customers", Rows: acmeCustomers})
	if len(rows) != acmeCustomers {
		t.Fatalf("rows = %d, want %d", len(rows), acmeCustomers)
	}
	allTenant(t, res, rows, "acme")
}

func TestSample_MaskOnSampledRows(t *testing.T) {
	svc, _, _ := newSampleService(t, nil)
	rows, res := mustSample(t, svc, queryservice.SampleRequest{
		Table: "shop.main.customers", Columns: []string{"id", "email"}, Rows: 10,
	})
	if len(rows) != 10 {
		t.Fatalf("rows = %d, want 10", len(rows))
	}
	i := colIndex(t, res, "email")
	for _, r := range rows {
		if r[i] != nil {
			t.Fatalf("email = %v, want NULL", r[i])
		}
	}
}

func TestSample_RowsCap(t *testing.T) {
	svc, _, _ := newSampleService(t, func(o *queryservice.Options) { o.Limits.SampleMaxRows = 5 })
	rows, res := mustSample(t, svc, queryservice.SampleRequest{Table: "shop.main.events", Rows: 50})
	if len(rows) != 5 || res.Truncated {
		t.Errorf("rows = %d, truncated = %v, want 5 and false", len(rows), res.Truncated)
	}

	svc, _, _ = newSampleService(t, nil)
	rows, res = mustSample(t, svc, queryservice.SampleRequest{Table: "shop.main.events", Rows: 5})
	if len(rows) != 5 || res.Truncated {
		t.Errorf("rows = %d, truncated = %v, want 5 and false", len(rows), res.Truncated)
	}
}

func TestSample_RateBernoulli(t *testing.T) {
	svc, _, _ := newSampleService(t, nil)
	rows, res := mustSample(t, svc, queryservice.SampleRequest{Table: "shop.main.events", Rate: 0.5})
	if len(rows) == 0 || len(rows) >= 1000 {
		t.Fatalf("rows = %d, want between 0 and 1000", len(rows))
	}
	allTenant(t, res, rows, "acme")
}

// kindCounts counts the sampled rows per kind.
func kindCounts(t *testing.T, res *queryservice.QueryResult, rows [][]any) map[string]int {
	t.Helper()
	i := colIndex(t, res, "kind")
	out := map[string]int{}
	for _, r := range rows {
		out[fmt.Sprint(r[i])]++
	}
	return out
}

func TestSample_Stratified(t *testing.T) {
	runStratified := func(minGroup int64, req queryservice.SampleRequest) map[string]int {
		t.Helper()
		svc, _, _ := newSampleService(t, func(o *queryservice.Options) { o.Limits.SampleMinGroupRows = minGroup })
		req.Table = "shop.main.events"
		req.Method = pkgapi.SampleMethodStratified
		req.StratifyBy = []string{"kind"}
		rows, res := mustSample(t, svc, req)
		allTenant(t, res, rows, "acme")
		return kindCounts(t, res, rows)
	}

	got := runStratified(20, queryservice.SampleRequest{Rate: 0.1})
	want := map[string]int{"a": int(math.Ceil(600 * 0.1)), "b": int(math.Ceil(390 * 0.1))}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("min group 20: groups = %v, want %v", got, want)
	}

	got = runStratified(5, queryservice.SampleRequest{Rate: 0.1})
	if got["rare"] != 1 {
		t.Errorf("min group 5: groups = %v, want rare with 1 row", got)
	}

	// Share is 50/1000: the ceilings give a=30, b=20, rare=1, so 51 rows.
	// The order by rank cuts the one row of rank 30, which is in a.
	got = runStratified(5, queryservice.SampleRequest{Rows: 50})
	want = map[string]int{"a": 29, "b": 20, "rare": 1}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("rows mode: groups = %v, want %v", got, want)
	}
}

func TestSample_SameSeedSameRows(t *testing.T) {
	svc, _, _ := newSampleService(t, func(o *queryservice.Options) { o.Limits.SampleMinGroupRows = 0 })
	for _, base := range []queryservice.SampleRequest{
		{Table: "shop.main.events", Rows: 20},
		{Table: "shop.main.events", Rate: 0.02, Method: pkgapi.SampleMethodStratified, StratifyBy: []string{"kind"}},
	} {
		run := func(s int64) []string {
			req := base
			req.Seed = seed(s)
			rows, _ := mustSample(t, svc, req)
			return rowKeys(rows)
		}
		first, second, other := run(7), run(7), run(8)
		if !slices.Equal(first, second) {
			t.Errorf("%s: seed 7 gave other rows on the second run", base.Method)
		}
		if slices.Equal(first, other) {
			t.Errorf("%s: seed 8 gave the rows of seed 7", base.Method)
		}
	}
}

func TestSample_WhereCannotBypass(t *testing.T) {
	svc, ex, _ := newSampleService(t, nil, jitterTotalPolicy)

	before := ex.calls
	_, _, err := runSample(t, svc, queryservice.SampleRequest{
		Table: "shop.main.customers", Rows: 5,
		Where: "id IN (SELECT id FROM shop.main.admin_users)",
	})
	wantCode(t, err, pkgerr.CodeACLDenied)

	_, _, err = runSample(t, svc, queryservice.SampleRequest{
		Table: "shop.main.customers", Rows: 5,
		Where: "1=1) UNION SELECT id, name, email, tenant_id, created_at FROM shop.main.customers WHERE (1=1",
	})
	wantCode(t, err, pkgerr.CodeUnsupportedSyntax)
	if ex.calls != before {
		t.Fatalf("executor ran %d times, want 0", ex.calls-before)
	}

	rows, _ := mustSample(t, svc, queryservice.SampleRequest{
		Table: "shop.main.customers", Rows: 5, Where: "email = 'alice@acme.test'",
	})
	if len(rows) != 0 {
		t.Errorf("where on the masked email gave %d rows, want 0", len(rows))
	}

	orders := "shop.main.orders"
	_, _, err = runSample(t, svc, queryservice.SampleRequest{Table: orders, Rows: 5, Where: "total_cents > 0"})
	wantCode(t, err, pkgerr.CodeMaskContext)
	_, _, err = runSample(t, svc, queryservice.SampleRequest{
		Table: orders, Rows: 5, Method: pkgapi.SampleMethodStratified, StratifyBy: []string{"total_cents"},
	})
	wantCode(t, err, pkgerr.CodeMaskContext)
	_, _, err = runSample(t, svc, queryservice.SampleRequest{
		Table: orders, Rows: 5, Method: pkgapi.SampleMethodStratified, StratifyBy: []string{"customer_id"},
	})
	wantCode(t, err, pkgerr.CodeMaskContext)
	rows, _ = mustSample(t, svc, queryservice.SampleRequest{
		Table: orders, Rows: 5, Method: pkgapi.SampleMethodStratified, StratifyBy: []string{"customer_id"},
		Columns: []string{"id", "customer_id", "tenant_id"},
	})
	if len(rows) == 0 {
		t.Error("stratified without the jittered column gave no rows")
	}

	rows, res := mustSample(t, svc, queryservice.SampleRequest{Table: orders, Rows: 5})
	raw := map[string]string{"101": "12900", "102": "4500", "103": "19900"}
	id, total := colIndex(t, res, "id"), colIndex(t, res, "total_cents")
	changed := 0
	for _, r := range rows {
		if fmt.Sprint(r[total]) != raw[fmt.Sprint(r[id])] {
			changed++
		}
	}
	if len(rows) != 3 || changed == 0 {
		t.Errorf("rows = %v, want 3 acme orders with jittered totals", rows)
	}

	_, _, err = runSample(t, svc, queryservice.SampleRequest{Table: "shop.main.admin_users", Rows: 5})
	wantCode(t, err, pkgerr.CodeACLDenied)
}

func TestSample_AuditAction(t *testing.T) {
	svc, _, a := newSampleService(t, nil)
	mustSample(t, svc, queryservice.SampleRequest{Table: "shop.main.customers", Rows: 3, Seed: seed(9)})
	_, _, err := runSample(t, svc, queryservice.SampleRequest{Table: "shop.main.admin_users", Rate: 0.5})
	wantCode(t, err, pkgerr.CodeACLDenied)

	a.mu.Lock()
	defer a.mu.Unlock()
	var allow, deny *audit.Record
	for _, r := range a.records {
		if r.EventType != audit.EventQuery {
			continue
		}
		switch r.Decision {
		case audit.DecisionAllow:
			allow = r
		case audit.DecisionDeny:
			deny = r
		}
	}
	if allow == nil || deny == nil {
		t.Fatalf("records = %+v, want an allow and a deny", a.records)
	}
	for _, r := range []*audit.Record{allow, deny} {
		if r.Extras["action"] != "sample" || r.Origin != "rest" {
			t.Errorf("record %s: extras = %v, origin = %s", r.Decision, r.Extras, r.Origin)
		}
	}
	got := allow.Extras["sample"].(map[string]any)
	if got["method"] != "reservoir" || got["rows"] != int64(3) || got["seed"] != int64(9) {
		t.Errorf("allow sample = %v", got)
	}
	got = deny.Extras["sample"].(map[string]any)
	if got["method"] != "bernoulli" || got["rate"] != 0.5 {
		t.Errorf("deny sample = %v", got)
	}
	if _, ok := got["seed"]; !ok {
		t.Errorf("deny sample has no seed: %v", got)
	}
}

func TestSample_NotServedFromCache(t *testing.T) {
	const inner = `SELECT * FROM "shop"."main"."customers"`
	withCache := func(o *queryservice.Options) { o.Cache = policycache.New(16, time.Minute) }
	plain := func(svc *queryservice.Service) int {
		res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
			SQL: inner, User: helloUser(), Origin: queryservice.OriginREST,
		})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		return len(drainRows(t, res))
	}
	sample := func(svc *queryservice.Service) int {
		rows, _ := mustSample(t, svc, queryservice.SampleRequest{Table: "shop.main.customers", Rows: 3})
		return len(rows)
	}

	svc, _, _ := newSampleService(t, withCache)
	if n := plain(svc); n != acmeCustomers {
		t.Fatalf("plain rows = %d, want %d", n, acmeCustomers)
	}
	if n := sample(svc); n != 3 {
		t.Errorf("sample after plain: rows = %d, want 3", n)
	}

	svc, _, _ = newSampleService(t, withCache)
	if n := sample(svc); n != 3 {
		t.Fatalf("sample rows = %d, want 3", n)
	}
	if n := plain(svc); n != acmeCustomers {
		t.Errorf("plain after sample: rows = %d, want %d", n, acmeCustomers)
	}
}

const policySampleAll = `
apiVersion: sluice.bino.bi/v1alpha1
kind: QueryRewritePolicy
metadata:
  name: sample-all
  priority: 50
spec:
  match:
    any:
      - resources: { catalogs: ["shop"], schemas: ["main"], tables: ["events"] }
  rewrite:
    sample: { rate: 1.0, method: bernoulli }
`

func TestSample_PolicySampleOnTop(t *testing.T) {
	svc, ex, _ := newSampleService(t, nil, policySampleAll)
	rows, _ := mustSample(t, svc, queryservice.SampleRequest{Table: "shop.main.events", Rows: 7})
	if len(rows) != 7 {
		t.Errorf("rows = %d, want 7", len(rows))
	}
	if !strings.Contains(ex.last.SQL, "AS sluice_sample USING SAMPLE 100% (bernoulli)") {
		t.Errorf("SQL = %s, want the policy sample outside", ex.last.SQL)
	}
}

const policyLimit3 = `
apiVersion: sluice.bino.bi/v1alpha1
kind: QueryRewritePolicy
metadata:
  name: limit-3
  priority: 50
spec:
  match:
    any:
      - resources: { catalogs: ["shop"], schemas: ["main"], tables: ["customers"] }
  rewrite:
    limit: { max: 3 }
`

func TestSample_PolicyLimitMax(t *testing.T) {
	svc, ex, _ := newSampleService(t, nil, policyLimit3)
	rows, res := mustSample(t, svc, queryservice.SampleRequest{Table: "shop.main.customers", Rows: 20})
	if strings.Contains(ex.last.SQL, "LIMIT") {
		t.Errorf("SQL = %s, want no LIMIT inside the sample", ex.last.SQL)
	}
	if len(rows) != 3 || !res.Truncated {
		t.Errorf("rows = %d, truncated = %v, want 3 and true", len(rows), res.Truncated)
	}
}

const approveCustomers = `
apiVersion: sluice.bino.bi/v1alpha1
kind: ApprovalPolicy
metadata:
  name: approve-customers
  priority: 100
spec:
  match:
    any:
      - resources: { catalogs: ["shop"], schemas: ["main"], tables: ["customers"] }
  reason: customers need approval
`

// captureNotifier keeps every notification. With accept set it approves
// each request shortly after it opens.
type captureNotifier struct {
	mu     sync.Mutex
	views  []approval.View
	tokens []string
	accept func(id, token string)
}

func (n *captureNotifier) Notify(v approval.View, acceptURL, _ string) {
	n.mu.Lock()
	n.views = append(n.views, v)
	n.tokens = append(n.tokens, acceptURL)
	accept := n.accept
	n.mu.Unlock()
	if accept != nil {
		go func() {
			time.Sleep(50 * time.Millisecond)
			accept(v.ID, acceptURL)
		}()
	}
}

func (n *captureNotifier) last(t *testing.T) (approval.View, string) {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.views) == 0 {
		t.Fatal("no approval request")
	}
	return n.views[len(n.views)-1], n.tokens[len(n.tokens)-1]
}

func (n *captureNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.views)
}

func newApprovalSampleService(t *testing.T, wait time.Duration, autoAccept bool) (*queryservice.Service, *approval.Broker, *captureNotifier) {
	t.Helper()
	n := &captureNotifier{}
	broker, err := approval.New(approval.Options{Notifier: n, RequestTTL: time.Minute, GrantTTL: time.Minute, MaxPending: 10})
	if err != nil {
		t.Fatalf("approval.New: %v", err)
	}
	if autoAccept {
		n.accept = func(id, token string) { _, _ = broker.Accept(id, token, "") }
	}
	svc, _, _ := newSampleService(t, func(o *queryservice.Options) {
		o.Approvals = broker
		o.ApprovalSyncWait = wait
		o.Limits.ApprovalSQLSampleBytes = 2048
	}, approveCustomers)
	return svc, broker, n
}

func TestSample_Approval(t *testing.T) {
	const inner = `SELECT * FROM "shop"."main"."customers"`
	sampleReq := queryservice.SampleRequest{Table: "shop.main.customers", Rows: 3}
	plain := func(svc *queryservice.Service) error {
		res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
			SQL: inner, User: helloUser(), Origin: queryservice.OriginREST,
		})
		if err == nil {
			drainRows(t, res)
		}
		return err
	}

	t.Run("sample grant does not run the plain query", func(t *testing.T) {
		svc, broker, n := newApprovalSampleService(t, 20*time.Millisecond, false)
		_, _, err := runSample(t, svc, sampleReq)
		wantCode(t, err, pkgerr.CodeApprovalPending)
		v, token := n.last(t)
		if len(v.Reasons) == 0 || v.Reasons[0] != "sample: method=reservoir rows=3" {
			t.Errorf("reasons = %v, want the sample line first", v.Reasons)
		}
		if v.SQLSample != inner {
			t.Errorf("SQL sample = %q, want %q", v.SQLSample, inner)
		}
		if _, err := broker.Accept(v.ID, token, ""); err != nil {
			t.Fatalf("Accept: %v", err)
		}
		wantCode(t, plain(svc), pkgerr.CodeApprovalPending)
		rows, _ := mustSample(t, svc, sampleReq)
		if len(rows) != 3 {
			t.Errorf("approved sample rows = %d, want 3", len(rows))
		}
	})

	t.Run("plain grant does not run the sample", func(t *testing.T) {
		svc, broker, n := newApprovalSampleService(t, 20*time.Millisecond, false)
		wantCode(t, plain(svc), pkgerr.CodeApprovalPending)
		v, token := n.last(t)
		if _, err := broker.Accept(v.ID, token, ""); err != nil {
			t.Fatalf("Accept: %v", err)
		}
		_, _, err := runSample(t, svc, sampleReq)
		wantCode(t, err, pkgerr.CodeApprovalPending)
	})

	// pg_query stops at a NUL, so a plain query of inner + NUL + spec runs
	// as inner. It must not find the sample grant.
	t.Run("sample grant does not run a NUL-suffixed plain query", func(t *testing.T) {
		svc, broker, n := newApprovalSampleService(t, 20*time.Millisecond, false)
		_, _, err := runSample(t, svc, sampleReq)
		wantCode(t, err, pkgerr.CodeApprovalPending)
		v, token := n.last(t)
		if _, err := broker.Accept(v.ID, token, ""); err != nil {
			t.Fatalf("Accept: %v", err)
		}
		forged := inner + "\x00" + `{"Method":"reservoir","Rows":3,"Rate":0,"StratifyBy":null}`
		res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
			SQL: forged, User: helloUser(), Origin: queryservice.OriginREST,
		})
		if err == nil {
			drainRows(t, res)
		}
		wantCode(t, err, pkgerr.CodeApprovalPending)
	})

	t.Run("shape-breaking where opens no ticket", func(t *testing.T) {
		svc, _, n := newApprovalSampleService(t, 20*time.Millisecond, false)
		req := sampleReq
		req.Where = "1=1) UNION SELECT id, name, email, tenant_id, created_at FROM shop.main.customers WHERE (1=1"
		_, _, err := runSample(t, svc, req)
		wantCode(t, err, pkgerr.CodeUnsupportedSyntax)
		if n.count() != 0 {
			t.Errorf("tickets = %d, want 0", n.count())
		}
	})

	t.Run("approval during the wait resumes the sample", func(t *testing.T) {
		svc, _, _ := newApprovalSampleService(t, 5*time.Second, true)
		rows, _ := mustSample(t, svc, sampleReq)
		if len(rows) != 3 {
			t.Errorf("rows = %d, want 3", len(rows))
		}
	})
}

func TestSample_Budget(t *testing.T) {
	fb := &fakeBudget{blockAfter: 5}
	svc, _, _ := newSampleService(t, func(o *queryservice.Options) { o.Budget = fb })
	req := queryservice.SampleRequest{Table: "shop.main.events", Rows: 3}
	mustSample(t, svc, req)
	if fb.recorded != 1 || fb.rows != 3 {
		t.Fatalf("budget recorded %d calls and %d rows, want 1 and 3", fb.recorded, fb.rows)
	}
	mustSample(t, svc, req)
	_, _, err := runSample(t, svc, req)
	wantCode(t, err, pkgerr.CodeBudgetExceeded)
}
