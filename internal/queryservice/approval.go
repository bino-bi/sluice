// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/bino-bi/sluice/internal/approval"
	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/policy"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

const approvalGuidance = "human approval required; a decision has been requested. " +
	"Poll or await the approval, then re-run the IDENTICAL query within the grant window to execute."

// awaitApproval parks the query on the broker, fires the webhook (via the
// broker's Notifier), and waits up to the sync window. The admission slot
// has already been released by the caller. On approval it re-runs the
// pipeline (resumed) which consumes the grant; otherwise it maps the
// terminal state to an APIError carrying the approval id.
func (s *Service) awaitApproval(
	ctx context.Context,
	req QueryRequest,
	dec *policy.Decision,
	rec *audit.Record,
	qid string,
	start time.Time,
	sqlHash, subjectKey string,
) (*QueryResult, error) {
	names := make([]string, 0, len(dec.Approval.Policies))
	for _, p := range dec.Approval.Policies {
		names = append(names, p.Name)
	}
	ticket, created, err := s.opts.Approvals.Require(ctx, approval.RequireInput{
		Subject:    approvalSubject(req.User),
		SubjectKey: subjectKey,
		SQLHash:    sqlHash,
		SQLSample:  sqlSampleFor(req.SQL, s.opts.Limits.ApprovalSQLSampleBytes),
		Reasons:    dec.Approval.Reasons,
		Policies:   names,
	})
	if err != nil {
		// Broker full — refuse loudly (fail-closed), mapped to 429.
		rec.Decision = audit.DecisionError
		rec.ErrorCode = string(pkgerr.CodeRateLimited)
		s.emit(ctx, rec, start)
		return nil, pkgerr.New(pkgerr.CodeRateLimited).WithQueryID(qid).
			WithMessage("approval queue full; try again later")
	}
	if created {
		// Best-effort lifecycle record; the webhook fired inside Require.
		reqRec := *rec
		reqRec.EventType = audit.EventApprovalRequested
		reqRec.Decision = audit.DecisionError
		if reqRec.Extras == nil {
			reqRec.Extras = map[string]any{}
		}
		reqRec.Extras["approval_id"] = ticket.ID
		reqRec.Extras["approval_policies"] = names
		s.emit(ctx, &reqRec, start)
	}

	wait := s.opts.ApprovalSyncWait
	if wait <= 0 {
		wait = 20 * time.Second
	}
	if dl, ok := ctx.Deadline(); ok {
		if budget := time.Until(dl) - 2*time.Second; budget < wait {
			wait = budget
		}
	}
	if wait < 0 {
		wait = 0
	}

	state, werr := s.opts.Approvals.Wait(ctx, ticket.ID, wait)
	if werr != nil {
		return nil, pkgerr.New(pkgerr.CodeApprovalPending).WithQueryID(qid).
			WithDetail("approval_id", ticket.ID).WithMessage(approvalGuidance)
	}

	switch state {
	case approval.StateApproved:
		// Grant was minted on accept; the resumed pass consumes it.
		return s.execute(ctx, req, true)
	case approval.StateRejected:
		rec.Decision = audit.DecisionDeny
		rec.ErrorCode = string(pkgerr.CodeApprovalRejected)
		s.emit(ctx, rec, start)
		return nil, pkgerr.New(pkgerr.CodeApprovalRejected).WithQueryID(qid).
			WithDetail("approval_id", ticket.ID)
	case approval.StateExpired:
		rec.Decision = audit.DecisionError
		rec.ErrorCode = string(pkgerr.CodeApprovalExpired)
		s.emit(ctx, rec, start)
		return nil, pkgerr.New(pkgerr.CodeApprovalExpired).WithQueryID(qid).
			WithDetail("approval_id", ticket.ID)
	default: // still pending after the sync wait
		rec.Decision = audit.DecisionError
		rec.ErrorCode = string(pkgerr.CodeApprovalPending)
		s.emit(ctx, rec, start)
		e := pkgerr.New(pkgerr.CodeApprovalPending).WithQueryID(qid).
			WithDetail("approval_id", ticket.ID).WithMessage(approvalGuidance)
		if v, ok := s.opts.Approvals.Get(ticket.ID); ok {
			e = e.WithDetail("expires_at", v.ExpiresAt.UTC().Format(time.RFC3339))
		}
		return nil, e
	}
}

// ApprovalStatus returns the state of an approval the caller owns. Used by
// the MCP check_approval / await_approval tools. Ownership is enforced by
// subject key so a caller cannot poll another subject's approval.
func (s *Service) ApprovalStatus(_ context.Context, user *identity.UserCtx, id string) (approval.View, error) {
	if s.opts.Approvals == nil {
		return approval.View{}, pkgerr.New(pkgerr.CodeInternal).WithMessage("approvals not configured")
	}
	v, ok := s.opts.Approvals.Get(id)
	if !ok || approval.SubjectKeyOf(v) != subjectKeyOf(user) {
		return approval.View{}, pkgerr.New(pkgerr.CodeApprovalExpired).WithMessage("approval not found")
	}
	return v, nil
}

// AwaitApproval blocks up to maxWait for a decision on an owned approval.
func (s *Service) AwaitApproval(ctx context.Context, user *identity.UserCtx, id string, maxWait time.Duration) (approval.View, error) {
	if s.opts.Approvals == nil {
		return approval.View{}, pkgerr.New(pkgerr.CodeInternal).WithMessage("approvals not configured")
	}
	v, ok := s.opts.Approvals.Get(id)
	if !ok || approval.SubjectKeyOf(v) != subjectKeyOf(user) {
		return approval.View{}, pkgerr.New(pkgerr.CodeApprovalExpired).WithMessage("approval not found")
	}
	if _, err := s.opts.Approvals.Wait(ctx, id, maxWait); err != nil {
		return approval.View{}, pkgerr.New(pkgerr.CodeApprovalPending).WithDetail("approval_id", id)
	}
	v, _ = s.opts.Approvals.Get(id)
	return v, nil
}

func sqlHashHex(sql string) string {
	sum := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(sum[:])
}

func subjectKeyOf(u *identity.UserCtx) string {
	if u == nil {
		return "\x00anonymous"
	}
	return u.Issuer + "\x00" + u.Subject
}

func approvalSubject(u *identity.UserCtx) approval.Subject {
	if u == nil {
		return approval.Subject{ID: "anonymous"}
	}
	return approval.Subject{ID: u.Subject, Issuer: u.Issuer, Email: u.Email, Groups: u.Groups}
}

// sqlSampleFor truncates sql to limit bytes. Zero or negative means no
// sample — the operator disabled it; there is no hidden fallback.
func sqlSampleFor(sql string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(sql) <= limit {
		return sql
	}
	return sql[:limit]
}
