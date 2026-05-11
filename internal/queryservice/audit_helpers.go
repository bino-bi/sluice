// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice

import (
	"context"
	"errors"
	"time"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	pkgapi "github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// buildAuditBase populates the record fields that are known before
// execution begins. Tables and catalogs come from the parser; the
// policies + decision are applied later once they're known.
func (s *Service) buildAuditBase(req QueryRequest, qid string, tables []parser.TableRef) *audit.Record {
	sample := ""
	if n := s.opts.Limits.SQLSampleBytes; n > 0 && len(req.SQL) > 0 {
		if n > len(req.SQL) {
			n = len(req.SQL)
		}
		sample = req.SQL[:n]
	}
	rec := &audit.Record{
		EventType: audit.EventQuery,
		QueryID:   qid,
		Origin:    string(req.Origin),
		SQLSample: sample,
		Subject:   subjectFromUser(req.User),
	}
	if req.User != nil {
		rec.RemoteIP = req.User.RemoteAddr
	}
	rec.Tables = tablesToStrings(tables)
	rec.Catalogs = catalogsFromTables(tables)
	return rec
}

// applyDecisionToAudit stamps the policy decision onto rec. Applied
// policies and rejection messages are flattened for forensics.
func applyDecisionToAudit(rec *audit.Record, dec *policy.Decision) {
	if dec == nil {
		return
	}
	rec.PoliciesApplied = append([]pkgapi.AppliedPolicy(nil), dec.Applied...)
	switch dec.Outcome {
	case policy.OutcomeAllow:
		rec.Decision = audit.DecisionAllow
	case policy.OutcomeDeny:
		rec.Decision = audit.DecisionDeny
		if dec.DenyReason != nil && dec.DenyReason.Message != "" {
			rec.Message = dec.DenyReason.Message
		}
	case policy.OutcomeReject:
		rec.Decision = audit.DecisionReject
		if len(dec.Rejections) > 0 {
			rec.Message = dec.Rejections[0].Message
		}
	}
}

// setErrorCode extracts the canonical error code from err and writes it
// onto the record. Non-APIErrors map to CodeInternal.
func setErrorCode(rec *audit.Record, err error) {
	if err == nil {
		return
	}
	apiErr := pkgerr.FromError(err)
	if apiErr == nil {
		rec.ErrorCode = string(pkgerr.CodeInternal)
		return
	}
	rec.ErrorCode = string(apiErr.Code)
	if apiErr.Message != "" && rec.Message == "" {
		rec.Message = apiErr.Message
	}
}

// subjectFromUser snapshots the relevant identity fields. Absent user →
// zero-value Subject so the record still carries structure.
func subjectFromUser(u *identity.UserCtx) audit.Subject {
	if u == nil {
		return audit.Subject{}
	}
	return audit.Subject{
		ID:       u.Subject,
		Method:   string(u.AuthMethod),
		Issuer:   u.Issuer,
		Email:    u.Email,
		Groups:   append([]string(nil), u.Groups...),
		RemoteIP: u.RemoteAddr,
	}
}

// tablesToStrings returns "catalog.schema.table" strings in parser
// order.
func tablesToStrings(ts []parser.TableRef) []string {
	if len(ts) == 0 {
		return nil
	}
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, tableKey(t))
	}
	return out
}

// catalogsFromTables returns the distinct catalog names referenced by
// ts, preserving first-occurrence order.
func catalogsFromTables(ts []parser.TableRef) []string {
	if len(ts) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ts))
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		if t.Catalog == "" {
			continue
		}
		if _, ok := seen[t.Catalog]; ok {
			continue
		}
		seen[t.Catalog] = struct{}{}
		out = append(out, t.Catalog)
	}
	return out
}

func tableKey(t parser.TableRef) string {
	switch {
	case t.Catalog != "" && t.Schema != "":
		return t.Catalog + "." + t.Schema + "." + t.Table
	case t.Schema != "":
		return t.Schema + "." + t.Table
	default:
		return t.Table
	}
}

// emit finalises durationMs on rec and pushes it onto the audit
// dispatcher. Enqueue errors are logged but not returned — we do not want
// to mask the real query error with an audit error on response paths.
func (s *Service) emit(ctx context.Context, rec *audit.Record, start time.Time) {
	rec.DurationMs = s.opts.Clock().Sub(start).Milliseconds()
	if err := s.opts.Audit.Enqueue(ctx, rec); err != nil {
		s.opts.Logger.Error("audit enqueue failed",
			"query_id", rec.QueryID,
			"decision", rec.Decision,
			"error", err.Error(),
		)
	}
}

// auditedRows is the RowIterator wrapper that emits exactly one audit
// record when the caller closes the iterator. Close is idempotent on the
// audit side: only the first Close emits.
type auditedRows struct {
	inner executor.RowIterator
	// owner / bookkeeping needed to finish the audit record.
	svc     *Service
	rec     *audit.Record
	start   time.Time
	closed  bool
	iterErr error
	parent  *QueryResult
}

func (r *auditedRows) Next() bool {
	if r.closed {
		return false
	}
	return r.inner.Next()
}

func (r *auditedRows) Scan(dest ...any) error {
	if r.closed {
		return errors.New("queryservice: Scan after Close")
	}
	if err := r.inner.Scan(dest...); err != nil {
		r.iterErr = err
		return err
	}
	return nil
}

func (r *auditedRows) Err() error {
	if r.iterErr != nil {
		return r.iterErr
	}
	return r.inner.Err()
}

func (r *auditedRows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	closeErr := r.inner.Close()

	// Row count & truncated are finalised on the inner iterator's Close.
	if r.parent != nil {
		if r.parent.RowCount != nil {
			r.rec.RowCount = *r.parent.RowCount
		}
		r.rec.Truncated = r.parent.Truncated
	}
	// Map iteration errors to the audit record (decision stays "allow"
	// but errorCode fills in).
	if r.iterErr != nil {
		r.rec.Decision = audit.DecisionError
		setErrorCode(r.rec, r.iterErr)
	} else if closeErr != nil {
		// Closing failed; record but keep decision=allow so the audit
		// still reflects the policy outcome accurately.
		setErrorCode(r.rec, closeErr)
	}
	r.svc.emit(context.Background(), r.rec, r.start)
	return closeErr
}
