// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/approval"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// fakeBroker implements the queryservice approvalBroker interface.
type fakeBroker struct {
	mu           sync.Mutex
	waitState    approval.State
	waitBlock    chan struct{} // if non-nil, Wait blocks until closed
	grantOK      bool
	requireCalls atomic.Int64
	waited       atomic.Int64
}

func (f *fakeBroker) Require(_ context.Context, _ approval.RequireInput) (approval.Ticket, bool, error) {
	f.requireCalls.Add(1)
	return approval.Ticket{ID: "apr_1", State: approval.StatePending, ExpiresAt: time.Unix(1_700_000_900, 0)}, true, nil
}

func (f *fakeBroker) Wait(ctx context.Context, _ string, _ time.Duration) (approval.State, error) {
	f.waited.Add(1)
	if f.waitBlock != nil {
		select {
		case <-f.waitBlock:
		case <-ctx.Done():
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.waitState, nil
}

func (f *fakeBroker) ConsumeGrant(_, _ string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.grantOK {
		f.grantOK = false // single-use
		return "apr_1", true
	}
	return "", false
}

func (f *fakeBroker) Get(string) (approval.View, bool) {
	return approval.View{ID: "apr_1", ExpiresAt: time.Unix(1_700_000_900, 0)}, true
}

func approvalDecision() *policy.Decision {
	return &policy.Decision{
		Outcome: policy.OutcomeAllow,
		Approval: &policy.ApprovalRequirement{
			Policies: []apitypes.AppliedPolicy{{Kind: apitypes.KindApprovalPolicy, Name: "gate"}},
			Reasons:  []string{"policy gate: needs sign-off"},
		},
	}
}

func newApprovalService(t *testing.T, broker queryservice.Options, dec *policy.Decision, ex *fakeExecutor) *queryservice.Service {
	t.Helper()
	broker.Parser = &fakeParser{}
	broker.Policy = &fakePolicy{decision: dec}
	broker.Rewriter = &fakeRewriter{}
	broker.Executor = ex
	broker.Audit = &fakeAudit{}
	broker.Clock = func() time.Time { return time.Unix(1713600000, 0) }
	return queryservice.New(broker)
}

func TestApproval_PendingWhenNotDecided(t *testing.T) {
	fb := &fakeBroker{waitState: approval.StatePending}
	svc := newApprovalService(t, queryservice.Options{Approvals: fb, ApprovalSyncWait: 10 * time.Millisecond},
		approvalDecision(), &fakeExecutor{})

	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT ssn FROM t"})
	ae := pkgerr.FromError(err)
	if ae == nil || ae.Code != pkgerr.CodeApprovalPending {
		t.Fatalf("err = %v, want ERR_APPROVAL_PENDING", err)
	}
	if ae.Details["approval_id"] != "apr_1" {
		t.Errorf("missing approval_id detail: %+v", ae.Details)
	}
	if fb.requireCalls.Load() != 1 {
		t.Errorf("Require called %d times, want 1", fb.requireCalls.Load())
	}
}

func TestApproval_RejectedMapsToForbidden(t *testing.T) {
	fb := &fakeBroker{waitState: approval.StateRejected}
	svc := newApprovalService(t, queryservice.Options{Approvals: fb, ApprovalSyncWait: 10 * time.Millisecond},
		approvalDecision(), &fakeExecutor{})
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT ssn FROM t"})
	ae := pkgerr.FromError(err)
	if ae == nil || ae.Code != pkgerr.CodeApprovalRejected {
		t.Fatalf("err = %v, want ERR_APPROVAL_REJECTED", err)
	}
}

func TestApproval_ApprovedResumesAndExecutes(t *testing.T) {
	// Wait returns approved; the resumed pass then consumes the grant and
	// executes.
	fb := &fakeBroker{waitState: approval.StateApproved, grantOK: true}
	ex := &fakeExecutor{columns: []executor.ColumnInfo{{Name: "ssn"}}, rows: [][]any{{"x"}}}
	svc := newApprovalService(t, queryservice.Options{Approvals: fb, ApprovalSyncWait: time.Second},
		approvalDecision(), ex)

	res, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT ssn FROM t"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	_ = res.Rows.Close()
	if ex.execCount != 1 {
		t.Errorf("executor ran %d times, want 1 (resumed pass)", ex.execCount)
	}
	if fb.grantOK {
		t.Error("grant not consumed")
	}
}

func TestApproval_GrantHitSkipsBroker(t *testing.T) {
	// A pre-existing grant lets the first pass proceed without parking.
	fb := &fakeBroker{grantOK: true}
	ex := &fakeExecutor{columns: []executor.ColumnInfo{{Name: "ssn"}}, rows: [][]any{{"x"}}}
	svc := newApprovalService(t, queryservice.Options{Approvals: fb, ApprovalSyncWait: time.Second},
		approvalDecision(), ex)

	res, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT ssn FROM t"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	_ = res.Rows.Close()
	if fb.requireCalls.Load() != 0 {
		t.Errorf("Require called %d times, want 0 (grant hit)", fb.requireCalls.Load())
	}
	if ex.execCount != 1 {
		t.Errorf("executor ran %d times, want 1", ex.execCount)
	}
}

func TestApproval_NilBrokerFailsClosed(t *testing.T) {
	svc := newApprovalService(t, queryservice.Options{}, approvalDecision(), &fakeExecutor{})
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT ssn FROM t"})
	ae := pkgerr.FromError(err)
	if ae == nil || ae.Code != pkgerr.CodeInternal {
		t.Fatalf("err = %v, want ERR_INTERNAL (fail-closed)", err)
	}
}

func TestApproval_SlotReleasedDuringWait(t *testing.T) {
	// With MaxConcurrent=1, a parked request must not hold the admission
	// slot: a second Execute must reach the broker (Require) while the
	// first is blocked in Wait. If the slot were held, the second would be
	// rejected at acquireSlot and Require would fire only once.
	fb := &fakeBroker{waitState: approval.StatePending, waitBlock: make(chan struct{})}
	ex := &fakeExecutor{columns: []executor.ColumnInfo{{Name: "id"}}}
	opts := queryservice.Options{Approvals: fb, ApprovalSyncWait: time.Second, Limits: queryservice.Limits{MaxConcurrent: 1}}
	svc := newApprovalService(t, opts, approvalDecision(), ex)

	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT ssn FROM t"})
			results <- err
		}()
	}

	// Both goroutines must reach the broker despite MaxConcurrent=1.
	deadline := time.After(2 * time.Second)
	for fb.requireCalls.Load() < 2 {
		select {
		case <-deadline:
			t.Fatalf("only %d requests reached the broker; slot not released while parked", fb.requireCalls.Load())
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	close(fb.waitBlock)
	for i := 0; i < 2; i++ {
		err := <-results
		if ae := pkgerr.FromError(err); ae == nil || ae.Code != pkgerr.CodeApprovalPending {
			t.Fatalf("request err = %v, want ERR_APPROVAL_PENDING", err)
		}
	}
}
