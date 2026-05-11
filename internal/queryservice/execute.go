// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/rewriter"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// Execute runs the full pipeline. On allow, it returns a QueryResult
// whose Rows iterator emits the success audit record when Close is
// called. On deny / reject / error, it emits the audit record inline
// and returns an APIError.
func (s *Service) Execute(ctx context.Context, req QueryRequest) (*QueryResult, error) {
	start := s.opts.Clock()
	qid := pkgerr.NewQueryID()

	// 0. Concurrency + input caps.
	if err := s.acquireSlot(ctx); err != nil {
		return nil, err
	}
	releaseSlot := s.releaseFn()

	if len(req.SQL) > s.opts.Limits.MaxSQLBytes {
		rec := s.buildAuditBase(req, qid, nil)
		rec.Decision = audit.DecisionError
		rec.ErrorCode = string(pkgerr.CodePayloadTooLarge)
		s.emit(ctx, rec, start)
		releaseSlot()
		return nil, pkgerr.New(pkgerr.CodePayloadTooLarge).WithQueryID(qid)
	}
	if req.Timeout <= 0 {
		req.Timeout = s.opts.Limits.DefaultTimeout
	}
	if req.MaxRows <= 0 {
		req.MaxRows = s.opts.Limits.DefaultMaxRows
	}

	// 1. Parse (best-effort; the regex fallback carries us when the
	// parser fails but tables are still extractable).
	ast, parseErr := s.opts.Parser.Parse(ctx, req.SQL)
	var shape parser.QueryShape
	var tables []parser.TableRef
	if parseErr == nil {
		shape = ast.Shape()
		tables = ast.Tables()
	} else {
		tables = parser.ExtractTablesRegex(req.SQL)
	}

	rec := s.buildAuditBase(req, qid, tables)
	if ast != nil {
		rec.SQLFingerprint = ast.Fingerprint()
	}

	// 2. Policy evaluation (on regex fallback, AST is nil but we still
	// evaluate against the extracted table list — default-deny kicks in
	// when policies scope the tables).
	dec, polErr := s.opts.Policy.Evaluate(ctx, policy.Input{
		User: req.User, AST: ast, Shape: shape, Tables: tables,
		Request: req.Facts, Now: start,
	})
	if polErr != nil {
		setErrorCode(rec, polErr)
		rec.Decision = audit.DecisionError
		s.emit(ctx, rec, start)
		releaseSlot()
		return nil, toAPIError(polErr, qid)
	}
	applyDecisionToAudit(rec, dec)

	// 3. Surface a parse error only when policy didn't already veto the
	// request. If policy denies or rejects, the deny/reject takes
	// precedence.
	if dec.Outcome == policy.OutcomeDeny {
		s.emit(ctx, rec, start)
		releaseSlot()
		return nil, denyToAPIError(dec, qid)
	}
	if dec.Outcome == policy.OutcomeReject {
		s.emit(ctx, rec, start)
		releaseSlot()
		return nil, rejectToAPIError(dec, qid)
	}

	// Parse errors after the policy decision — a successful Allow still
	// requires a parseable query to proceed.
	if parseErr != nil {
		setErrorCode(rec, parseErr)
		rec.Decision = audit.DecisionError
		s.emit(ctx, rec, start)
		releaseSlot()
		return nil, parseErrToAPI(parseErr, qid)
	}

	// 4. Rewrite.
	rewriteResp, reErr := s.opts.Rewriter.Rewrite(ctx, rewriter.RewriteRequest{
		AST: ast, Decision: dec, User: req.User, Facts: req.Facts, Raw: req.SQL,
	})
	if reErr != nil {
		setErrorCode(rec, reErr)
		rec.Decision = audit.DecisionError
		s.emit(ctx, rec, start)
		releaseSlot()
		return nil, toAPIError(reErr, qid)
	}
	rec.RewrittenFingerprint = rewriteResp.Fingerprint

	// 5. Execute.
	execReq := executor.Request{
		QueryID: qid,
		SQL:     rewriteResp.SQL,
		Params:  rewriteResp.Params,
		MaxRows: req.MaxRows,
		Timeout: req.Timeout,
		Format:  req.Format,
	}
	resp, execErr := s.opts.Executor.Execute(ctx, execReq)
	if execErr != nil {
		setErrorCode(rec, execErr)
		rec.Decision = audit.DecisionError
		s.emit(ctx, rec, start)
		releaseSlot()
		return nil, execErrToAPI(execErr, qid)
	}

	// 6. Wrap rows: audit emission deferred until iterator.Close.
	qr := &QueryResult{
		QueryID:    qid,
		Columns:    resp.Columns,
		RowCount:   resp.RowCount,
		Truncated:  resp.Truncated,
		Rewrites:   rewriteResp.Rewrites,
		Applied:    dec.Applied,
		Decision:   audit.DecisionAllow,
		DurationMs: 0,
	}
	qr.Rows = &auditedRows{
		inner:  resp.Rows,
		svc:    s,
		rec:    rec,
		start:  start,
		parent: qr,
	}
	// Release concurrency slot when the iterator is closed — but don't
	// leak semaphore slots if the caller forgets. We release now because
	// DuckDB holds its own connection pool slot for the duration, and
	// Service.MaxConcurrent is a higher-level gate on request
	// throughput, not on executor resources.
	releaseSlot()
	return qr, nil
}

// acquireSlot grabs a semaphore token when MaxConcurrent is set. Returns
// ERR_RATE_LIMITED when the limit is reached and the context's deadline
// fires (or immediately when no deadline is set).
func (s *Service) acquireSlot(ctx context.Context) error {
	if s.sem == nil {
		return nil
	}
	select {
	case s.sem <- struct{}{}:
		return nil
	default:
	}
	// Wait up to ctx or a short grace period.
	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()
	select {
	case s.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return pkgerr.New(pkgerr.CodeRateLimited).WithMessage("service saturated")
	case <-timer.C:
		return pkgerr.New(pkgerr.CodeRateLimited).WithMessage("service saturated")
	}
}

func (s *Service) releaseFn() func() {
	if s.sem == nil {
		return func() {}
	}
	released := false
	return func() {
		if released {
			return
		}
		released = true
		<-s.sem
	}
}

// denyToAPIError maps a policy deny decision to an APIError.
func denyToAPIError(dec *policy.Decision, qid string) error {
	e := pkgerr.New(pkgerr.CodeACLDenied).WithQueryID(qid)
	if dec.DenyReason != nil {
		if dec.DenyReason.Message != "" {
			e = e.WithMessage(dec.DenyReason.Message)
		}
		if dec.DenyReason.PolicyName != "" {
			e = e.WithPolicy(dec.DenyReason.PolicyName)
		}
		if dec.DenyReason.Code != "" {
			// DenyReason.Code uses pkg/errors string codes.
			e.Code = pkgerr.Code(dec.DenyReason.Code)
		}
	}
	return e
}

func rejectToAPIError(dec *policy.Decision, qid string) error {
	e := pkgerr.New(pkgerr.CodeACLRejected).WithQueryID(qid)
	if len(dec.Rejections) > 0 {
		top := dec.Rejections[0]
		if top.Message != "" {
			e = e.WithMessage(top.Message)
		}
		if top.PolicyName != "" {
			e = e.WithPolicy(top.PolicyName)
		}
		if top.Code != "" {
			e.Code = pkgerr.Code(top.Code)
		}
	}
	return e
}

// toAPIError is the fallback used for rewriter / policy / unexpected
// errors. It preserves an existing APIError, otherwise maps sentinels to
// canonical codes.
func toAPIError(err error, qid string) error {
	if err == nil {
		return nil
	}
	if ae := pkgerr.FromError(err); ae != nil && ae.Code != pkgerr.CodeInternal {
		return ae.WithQueryID(qid)
	}
	// Rewriter sentinels.
	switch {
	case stderrors.Is(err, rewriter.ErrUnsupportedSyntax):
		return pkgerr.Wrap(pkgerr.CodeUnsupportedSyntax, err).WithQueryID(qid)
	case stderrors.Is(err, rewriter.ErrDeparseFailed):
		return pkgerr.Wrap(pkgerr.CodeRewriteFailed, err).WithQueryID(qid)
	case stderrors.Is(err, rewriter.ErrStatementRejected):
		return pkgerr.New(pkgerr.CodeACLRejected).WithQueryID(qid)
	case stderrors.Is(err, policy.ErrDeny):
		return pkgerr.New(pkgerr.CodeACLDenied).WithQueryID(qid)
	case stderrors.Is(err, policy.ErrReject):
		return pkgerr.New(pkgerr.CodeACLRejected).WithQueryID(qid)
	}
	return pkgerr.Wrap(pkgerr.CodeInternal, err).WithQueryID(qid)
}

func parseErrToAPI(err error, qid string) error {
	if err == nil {
		return nil
	}
	switch {
	case stderrors.Is(err, parser.ErrMultipleStatements):
		return pkgerr.Wrap(pkgerr.CodeMultipleStatements, err).WithQueryID(qid)
	case stderrors.Is(err, parser.ErrInputTooLarge):
		return pkgerr.Wrap(pkgerr.CodePayloadTooLarge, err).WithQueryID(qid)
	case stderrors.Is(err, parser.ErrUnsupported):
		return pkgerr.Wrap(pkgerr.CodeUnsupportedSyntax, err).WithQueryID(qid)
	case stderrors.Is(err, parser.ErrSyntax):
		return pkgerr.Wrap(pkgerr.CodeSyntax, err).WithQueryID(qid)
	}
	return pkgerr.Wrap(pkgerr.CodeSyntax, err).WithQueryID(qid)
}

func execErrToAPI(err error, qid string) error {
	if err == nil {
		return nil
	}
	switch {
	case stderrors.Is(err, context.Canceled), stderrors.Is(err, executor.ErrCanceled):
		return pkgerr.Wrap(pkgerr.CodeCanceled, err).WithQueryID(qid)
	case stderrors.Is(err, context.DeadlineExceeded):
		return pkgerr.Wrap(pkgerr.CodeTimeout, err).WithQueryID(qid)
	}
	return pkgerr.Wrap(pkgerr.CodeInternal, err).WithQueryID(qid)
}
