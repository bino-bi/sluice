// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice_test

import (
	"context"
	"testing"

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
