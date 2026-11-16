// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/internal/rewriter"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// panicRewriter panics on the first call, like a bug in a cgo or plugin
// stage would.
type panicRewriter struct{ calls int }

func (r *panicRewriter) Rewrite(_ context.Context, req rewriter.RewriteRequest) (*rewriter.RewriteResult, error) {
	r.calls++
	if r.calls == 1 {
		panic("boom")
	}
	return &rewriter.RewriteResult{SQL: req.Raw, Fingerprint: "fp-out"}, nil
}

// A panic in a stage gives ERR_INTERNAL with an audit record and frees the
// admission slot, so the next request is not refused as saturated.
func TestExecute_PanicReleasesSlotAndAudits(t *testing.T) {
	a := &fakeAudit{}
	svc := queryservice.New(queryservice.Options{
		Parser:   &fakeParser{},
		Policy:   &fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		Rewriter: &panicRewriter{},
		Executor: &fakeExecutor{columns: []executor.ColumnInfo{{Name: "id"}}},
		Audit:    a,
		Limits:   queryservice.Limits{MaxConcurrent: 1},
	})
	req := queryservice.QueryRequest{SQL: "SELECT id FROM pg.public.orders", Origin: queryservice.OriginREST}
	_, err := svc.Execute(context.Background(), req)
	ae := pkgerr.FromError(err)
	if err == nil || ae.Code != pkgerr.CodeInternal || ae.QueryID == "" {
		t.Fatalf("err = %v, want ERR_INTERNAL with a query id", err)
	}
	recs := a.all()
	if len(recs) != 1 || recs[0].ErrorCode != string(pkgerr.CodeInternal) || recs[0].QueryID != ae.QueryID ||
		recs[0].Decision != audit.DecisionError || recs[0].SQLFingerprint == "" {
		t.Fatalf("records = %+v, want one ERR_INTERNAL error record of the request", recs)
	}
	res, err := svc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("second execute: %v, want the slot free again", err)
	}
	_ = res.Rows.Close()
}

// A wait that ends with an error, for example because the client went
// away, still leaves the decision record of the request.
func TestApproval_WaitErrorIsAudited(t *testing.T) {
	a := &fakeAudit{}
	fb := &fakeBroker{waitErr: context.Canceled}
	svc := queryservice.New(queryservice.Options{
		Parser:           &fakeParser{},
		Policy:           &fakePolicy{decision: approvalDecision()},
		Rewriter:         &fakeRewriter{},
		Executor:         &fakeExecutor{},
		Audit:            a,
		Approvals:        fb,
		ApprovalSyncWait: 10 * time.Millisecond,
	})
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT ssn FROM t"})
	ae := pkgerr.FromError(err)
	if ae == nil || ae.Code != pkgerr.CodeApprovalPending {
		t.Fatalf("err = %v, want ERR_APPROVAL_PENDING", err)
	}
	var decision []*audit.Record
	for _, r := range a.all() {
		if r.EventType == audit.EventQuery {
			decision = append(decision, r)
		}
	}
	if len(decision) != 1 || decision[0].ErrorCode != string(pkgerr.CodeApprovalPending) || decision[0].QueryID != ae.QueryID {
		t.Fatalf("decision records = %+v, want one with ERR_APPROVAL_PENDING", decision)
	}
}

// A stream cut by the query timeout ends without a Scan error. The
// completion record must still say so, not report a clean allow.
func TestAuditedRows_TimeoutDuringDeliveryIsAudited(t *testing.T) {
	a := &fakeAudit{}
	svc := buildService(t, &fakeParser{},
		&fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		&fakeRewriter{},
		&fakeExecutor{
			columns: []executor.ColumnInfo{{Name: "id"}},
			rows:    [][]any{{int64(1)}},
			iterErr: fmt.Errorf("%w: %w", executor.ErrExecute, context.DeadlineExceeded),
		},
		a,
	)
	res, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT id FROM pg.public.orders"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for res.Rows.Next() {
		var v any
		_ = res.Rows.Scan(&v)
	}
	_ = res.Rows.Close()
	recs := a.all()
	if len(recs) != 2 {
		t.Fatalf("got %d records, want access and completion", len(recs))
	}
	comp := recs[1]
	if comp.EventType != audit.EventQueryResult || comp.Decision != audit.DecisionError || comp.ErrorCode != string(pkgerr.CodeTimeout) {
		t.Fatalf("completion record: event %s decision %s code %q, want query-result, error, ERR_TIMEOUT", comp.EventType, comp.Decision, comp.ErrorCode)
	}
}
