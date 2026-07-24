// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice

import (
	"context"
	stderrors "errors"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/policycache"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/internal/schema"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// Execute runs the full pipeline. On allow, it returns a QueryResult
// whose Rows iterator emits the success audit record when Close is
// called. On deny / reject / error, it emits the audit record inline
// and returns an APIError.
func (s *Service) Execute(ctx context.Context, req QueryRequest) (*QueryResult, error) {
	return s.execute(ctx, req, false)
}

// execute is the pipeline body. resumed is true on the second pass after
// an approval was granted: it consumes the grant instead of parking again,
// which bounds recursion to one level.
func (s *Service) execute(ctx context.Context, req QueryRequest, resumed bool) (*QueryResult, error) {
	start := s.opts.Clock()
	qid := pkgerr.NewQueryID()

	// Enclosing pipeline span. Attributes carry identifiers and codes
	// only — never raw SQL, never secret bytes. finalRec is stamped onto
	// the span at End so every exit path reports its decision.
	ctx, span := s.tracer.Start(ctx, "query", trace.WithAttributes(
		attribute.String("sluice.query.id", qid),
		attribute.String("sluice.query.origin", string(req.Origin)),
	))
	var finalRec *audit.Record
	defer func() { endQuerySpan(span, finalRec) }()

	// 0. Concurrency + input caps.
	if err := s.acquireSlot(ctx); err != nil {
		return nil, err
	}
	releaseSlot := s.releaseFn()

	// 0b. Per-subject rate limit.
	if s.opts.RateLimiter != nil && req.User != nil {
		if !s.opts.RateLimiter.Allow(req.User.Subject, req.User.Issuer) {
			rec := s.buildAuditBase(req, qid, nil)
			finalRec = rec
			rec.Decision = audit.DecisionError
			rec.ErrorCode = string(pkgerr.CodeRateLimited)
			s.emit(ctx, rec, start)
			releaseSlot()
			return nil, pkgerr.New(pkgerr.CodeRateLimited).
				WithQueryID(qid).
				WithMessage("per-subject rate limit exceeded")
		}
	}

	// 0c. Per-subject daily budget (pre-admission). A subject over its
	// budget is refused before any work. Usage is recorded post-execution.
	if s.opts.Budget != nil && req.User != nil {
		if err := s.opts.Budget.Check(ctx, req.User.Subject, req.User.Issuer); err != nil {
			rec := s.buildAuditBase(req, qid, nil)
			finalRec = rec
			rec.Decision = audit.DecisionError
			rec.ErrorCode = string(pkgerr.CodeBudgetExceeded)
			s.emit(ctx, rec, start)
			releaseSlot()
			return nil, pkgerr.FromError(err).WithQueryID(qid)
		}
	}

	if len(req.SQL) > s.opts.Limits.MaxSQLBytes {
		rec := s.buildAuditBase(req, qid, nil)
		finalRec = rec
		rec.Decision = audit.DecisionError
		rec.ErrorCode = string(pkgerr.CodePayloadTooLarge)
		s.emit(ctx, rec, start)
		releaseSlot()
		return nil, pkgerr.New(pkgerr.CodePayloadTooLarge).WithQueryID(qid)
	}
	if req.Timeout <= 0 {
		req.Timeout = s.opts.Limits.DefaultTimeout
	}
	if req.Timeout > s.opts.Limits.MaxTimeout {
		req.Timeout = s.opts.Limits.MaxTimeout
	}
	if req.MaxRows <= 0 {
		req.MaxRows = s.opts.Limits.DefaultMaxRows
	}
	if req.MaxRows > s.opts.Limits.MaxRowsCeiling {
		req.MaxRows = s.opts.Limits.MaxRowsCeiling
	}

	// 1. Parse (best-effort; the regex fallback carries us when the
	// parser fails but tables are still extractable).
	parseCtx, parseSpan := s.tracer.Start(ctx, "query.parse")
	ast, parseErr := s.opts.Parser.Parse(parseCtx, req.SQL)
	var shape parser.QueryShape
	var tables []parser.TableRef
	if parseErr == nil {
		shape = ast.Shape()
		tables = ast.Tables()
	} else {
		tables = parser.ExtractTablesRegex(req.SQL)
	}
	parseSpan.End()

	rec := s.buildAuditBase(req, qid, tables)
	finalRec = rec
	if ast != nil {
		rec.SQLFingerprint = ast.Fingerprint()
	}

	// 1b. Cross-catalog gate. Runs before the rewrite cache and policy so
	// a memoised Allow cannot bypass it. Only three-part
	// (catalog.schema.table) names carry a catalog, matching the policy
	// rule size(query.catalogs) > 1.
	if s.opts.Limits.DisableCrossCatalog {
		if cats := catalogsFromTables(tables); len(cats) > 1 {
			rec.Decision = audit.DecisionReject
			rec.Message = "cross-catalog queries are disabled (limits.disableCrossCatalog)"
			s.emit(ctx, rec, start)
			releaseSlot()
			return nil, pkgerr.New(pkgerr.CodeACLRejected).WithQueryID(qid).
				WithMessage("cross-catalog queries are disabled (limits.disableCrossCatalog)")
		}
	}

	// 2. Policy evaluation + rewrite, memoised when the cache is enabled.
	// The key binds the raw SQL text and full identity to the active
	// snapshot; a hit skips both Evaluate and Rewrite. Everything else
	// (rate limit, budget, deny/reject short-circuit, clamps, post-masks,
	// audit) still runs per request. Only a clean parse is cacheable.
	cacheKey, cacheable := s.cacheKey(req, parseErr)
	var (
		dec         *policy.Decision
		rewriteResp *rewriter.RewriteResult
		fromCache   bool
	)
	if cacheable {
		if entry, ok := s.opts.Cache.Get(cacheKey); ok {
			dec = entry.Decision
			rewriteResp = entry.Rewrite
			fromCache = true
		}
	}
	if dec == nil {
		polCtx, polSpan := s.tracer.Start(ctx, "query.policy")
		var polErr error
		dec, polErr = s.opts.Policy.Evaluate(polCtx, policy.Input{
			User: req.User, AST: ast, Shape: shape, Tables: tables,
			Request: req.Facts, Now: start,
		})
		polSpan.End()
		if polErr != nil {
			setErrorCode(rec, polErr)
			rec.Decision = audit.DecisionError
			s.emit(ctx, rec, start)
			releaseSlot()
			return nil, toAPIError(polErr, qid)
		}
	}
	applyDecisionToAudit(rec, dec)

	// 3. Surface a parse error only when policy didn't already veto the
	// request. If policy denies or rejects, the deny/reject takes
	// precedence.
	if dec.Outcome == policy.OutcomeDeny {
		if cacheable && !fromCache {
			s.opts.Cache.Put(cacheKey, &policycache.Entry{Decision: dec})
		}
		s.emit(ctx, rec, start)
		releaseSlot()
		return nil, denyToAPIError(dec, qid)
	}
	if dec.Outcome == policy.OutcomeReject {
		if cacheable && !fromCache {
			s.opts.Cache.Put(cacheKey, &policycache.Entry{Decision: dec})
		}
		s.emit(ctx, rec, start)
		releaseSlot()
		return nil, rejectToAPIError(dec, qid)
	}

	// Parse errors after the policy decision — a successful Allow still
	// requires a parseable query to proceed. (Unreachable on a cache hit:
	// caching requires a clean parse.)
	if parseErr != nil {
		setErrorCode(rec, parseErr)
		rec.Decision = audit.DecisionError
		s.emit(ctx, rec, start)
		releaseSlot()
		return nil, parseErrToAPI(parseErr, qid)
	}

	// 3b. Approval gate. A decision carrying an approval requirement holds
	// the query until a human decides. On a consumed grant we proceed and
	// note the approval id; otherwise we park (releasing the admission slot
	// first) and either resolve inline or return ERR_APPROVAL_PENDING.
	if dec.Approval != nil {
		if s.opts.Approvals == nil {
			// Policy requires approval but no broker is wired — fail closed.
			rec.Decision = audit.DecisionError
			rec.ErrorCode = string(pkgerr.CodeInternal)
			s.emit(ctx, rec, start)
			releaseSlot()
			return nil, pkgerr.New(pkgerr.CodeInternal).WithQueryID(qid).
				WithMessage("approval required but no approval broker configured")
		}
		sqlHash := sqlHashHex(req.SQL)
		subjectKey := subjectKeyOf(req.User)
		if apprID, ok := s.opts.Approvals.ConsumeGrant(subjectKey, sqlHash); ok {
			if rec.Extras == nil {
				rec.Extras = map[string]any{}
			}
			rec.Extras["approval_id"] = apprID
			// Fall through to rewrite + execute with the grant consumed.
		} else if resumed {
			// Resumed pass but the grant vanished (expired/raced). Do not
			// re-park — that would recurse. Surface pending.
			s.emit(ctx, rec, start)
			releaseSlot()
			return nil, pkgerr.New(pkgerr.CodeApprovalPending).WithQueryID(qid)
		} else {
			releaseSlot() // never hold the admission slot while parked
			return s.awaitApproval(ctx, req, dec, rec, qid, start, sqlHash, subjectKey)
		}
	}

	// 4. Rewrite (skipped on a cache hit, which already carries the result).
	if rewriteResp == nil {
		rwCtx, rwSpan := s.tracer.Start(ctx, "query.rewrite")
		var reErr error
		rewriteResp, reErr = s.opts.Rewriter.Rewrite(rwCtx, rewriter.RewriteRequest{
			AST: ast, Decision: dec, User: req.User, Facts: req.Facts, Raw: req.SQL,
		})
		rwSpan.End()
		if reErr != nil {
			// Never cache error paths.
			setErrorCode(rec, reErr)
			rec.Decision = audit.DecisionError
			s.emit(ctx, rec, start)
			releaseSlot()
			return nil, toAPIError(reErr, qid)
		}
		if cacheable {
			s.opts.Cache.Put(cacheKey, &policycache.Entry{Decision: dec, Rewrite: rewriteResp})
		}
	}
	rec.RewrittenFingerprint = rewriteResp.Fingerprint

	// 4b. QueryRewritePolicy effects that live outside the SQL: the
	// timeout override and the belt-and-braces row cap. Both only ever
	// tighten — the ceilings clamped in step 0 still hold.
	if eff := dec.Rewrite; eff != nil {
		if eff.Timeout > 0 && eff.Timeout < req.Timeout {
			req.Timeout = eff.Timeout
		}
		if eff.LimitMax > 0 && eff.LimitMax < req.MaxRows {
			req.MaxRows = eff.LimitMax
		}
		if rec.Extras == nil {
			rec.Extras = map[string]any{}
		}
		rec.Extras["rewrite"] = map[string]any{
			"limit":      eff.LimitMax,
			"timeout_ms": eff.Timeout.Milliseconds(),
			"sample":     eff.Sample != nil,
			"policies":   eff.Policies,
		}
	}

	// 5. Execute.
	execReq := executor.Request{
		QueryID: qid,
		SQL:     rewriteResp.SQL,
		Params:  rewriteResp.Params,
		MaxRows: req.MaxRows,
		Timeout: req.Timeout,
		Format:  req.Format,
	}
	execCtx, execSpan := s.tracer.Start(ctx, "query.execute")
	resp, execErr := s.opts.Executor.Execute(execCtx, execReq)
	execSpan.End()
	if execErr != nil {
		setErrorCode(rec, execErr)
		rec.Decision = audit.DecisionError
		s.emit(ctx, rec, start)
		releaseSlot()
		return nil, execErrToAPI(execErr, qid)
	}

	// 5b. Post-query mask decorators (FPE, fake, jitter, hmac). Build them
	// before the audit gate so a construction failure refuses the query
	// with nothing served and nothing falsely audited as allowed.
	if len(rewriteResp.PostMasks) > 0 {
		maskCtx, maskSpan := s.tracer.Start(ctx, "query.mask")
		masked, mErr := s.buildMaskedRows(maskCtx, resp.Rows, identityView{req.User}, rewriteResp.PostMasks)
		maskSpan.End()
		if mErr != nil {
			_ = resp.Rows.Close()
			setErrorCode(rec, mErr)
			rec.Decision = audit.DecisionError
			s.emit(ctx, rec, start)
			releaseSlot()
			return nil, toAPIError(mErr, qid)
		}
		resp.Rows = masked
		if rec.Extras == nil {
			rec.Extras = map[string]any{}
		}
		labels := make([]string, 0, len(rewriteResp.PostMasks))
		for _, pm := range rewriteResp.PostMasks {
			labels = append(labels, pm.TableKey+"."+pm.Column+"="+string(pm.Type))
		}
		rec.Extras["post_masks"] = labels
	}

	// 6. Fail-closed audit gate. Durably record the access decision BEFORE
	// any row reaches the caller. When fail-closed (the default) an enqueue
	// failure refuses the query so no data is served unaudited. A completion
	// record carrying the final RowCount is emitted best-effort at Close.
	rec.Decision = audit.DecisionAllow
	if err := s.emitAccess(ctx, rec, start); err != nil {
		_ = resp.Rows.Close()
		releaseSlot()
		return nil, err
	}

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
	ar := &auditedRows{
		inner:    resp.Rows,
		svc:      s,
		qid:      qid,
		start:    start,
		parent:   qr,
		truncSrc: &resp.Truncated,
	}
	// Budget usage: capture the execute duration now (excludes client
	// streaming so a slow reader does not burn budget); rows are known at
	// Close. Record fires once in Close.
	if s.opts.Budget != nil && req.User != nil {
		ar.budgetSubject = req.User.Subject
		ar.budgetIssuer = req.User.Issuer
		ar.execDur = s.opts.Clock().Sub(start)
		ar.recordBudget = true
	}
	qr.Rows = ar
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
	case stderrors.Is(err, rewriter.ErrMaskPostQueryContext):
		return pkgerr.Wrap(pkgerr.CodeMaskContext, err).WithQueryID(qid)
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
	case stderrors.Is(err, schema.ErrUnknownCatalog):
		return pkgerr.Wrap(pkgerr.CodeDataSourceUnavailable, err).WithQueryID(qid)
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

// endQuerySpan stamps the finished pipeline's outcome onto the span and
// ends it. Only identifiers, fingerprints, and error codes — never raw
// SQL or secret bytes.
func endQuerySpan(span trace.Span, rec *audit.Record) {
	if rec != nil {
		if rec.Decision != "" {
			span.SetAttributes(attribute.String("sluice.query.decision", rec.Decision))
		}
		if rec.SQLFingerprint != "" {
			span.SetAttributes(attribute.String("sluice.sql.fingerprint", rec.SQLFingerprint))
		}
		if len(rec.Catalogs) > 0 {
			span.SetAttributes(attribute.StringSlice("sluice.query.catalogs", rec.Catalogs))
		}
		if rec.ErrorCode != "" {
			span.SetAttributes(attribute.String("sluice.query.error_code", rec.ErrorCode))
			span.SetStatus(codes.Error, rec.ErrorCode)
		}
	}
	span.End()
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
	case stderrors.Is(err, executor.ErrAttach), stderrors.Is(err, executor.ErrConnUnavailable):
		return pkgerr.Wrap(pkgerr.CodeDataSourceUnavailable, err).WithQueryID(qid)
	}
	return pkgerr.Wrap(pkgerr.CodeInternal, err).WithQueryID(qid)
}
