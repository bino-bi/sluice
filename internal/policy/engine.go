// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// Options configures a new Engine.
type Options struct {
	Clock  func() time.Time
	Logger *slog.Logger
}

// Engine is the policy evaluator. It is safe for concurrent use. Writers
// swap the active snapshot atomically; readers see a consistent view for
// the duration of a single Evaluate call.
type Engine struct {
	snapshot atomic.Pointer[CompiledSnapshot]
	clock    func() time.Time
	logger   *slog.Logger
}

// New returns an Engine with no active snapshot. Evaluate denies every
// request until ApplySnapshot succeeds — matching the default-deny
// posture.
func New(opts Options) *Engine {
	e := &Engine{
		clock:  opts.Clock,
		logger: opts.Logger,
	}
	if e.clock == nil {
		e.clock = time.Now
	}
	if e.logger == nil {
		e.logger = slog.Default()
	}
	return e
}

// Snapshot returns the currently-active compiled snapshot. Callers must
// treat the result as immutable.
func (e *Engine) Snapshot() *CompiledSnapshot {
	return e.snapshot.Load()
}

// ApplySnapshot compiles src and swaps it into place. On failure the
// previous snapshot stays live and the error is returned.
func (e *Engine) ApplySnapshot(ctx context.Context, src *config.Snapshot) error {
	compiled, err := Compile(ctx, src)
	if err != nil {
		return err
	}
	e.snapshot.Store(compiled)
	return nil
}

// Input is the per-request evaluation input.
type Input struct {
	User    *identity.UserCtx
	AST     parser.AST
	Shape   parser.QueryShape
	Tables  []parser.TableRef
	Request *RequestFacts
	Now     time.Time
}

// Evaluate produces a Decision for the given input. The returned
// Decision is owned by the caller and may be mutated freely.
func (e *Engine) Evaluate(_ context.Context, in Input) (*Decision, error) {
	start := e.clock()
	snap := e.snapshot.Load()
	if snap == nil {
		dec := &Decision{
			Outcome: OutcomeDeny,
			DenyReason: &DenyReason{
				Message: "no policy snapshot active (default-deny)",
				Code:    "ACL_DENIED",
			},
		}
		globalMetrics.evaluated(OutcomeDeny)
		globalMetrics.observe(e.clock().Sub(start).Seconds())
		return dec, nil
	}

	act := actionFromInput(in)
	matched := e.selectMatching(snap, in, act)
	dec := resolve(matched, in.Tables, in.User, act)
	dec.Evaluated = len(snap.Policies)
	dec.Duration = e.clock().Sub(start)

	globalMetrics.evaluated(dec.Outcome)
	globalMetrics.observe(dec.Duration.Seconds())
	if dec.Outcome == OutcomeDeny && dec.DenyReason != nil {
		globalMetrics.denied(dec.DenyReason.PolicyName)
	}
	for _, r := range dec.Rejections {
		globalMetrics.rejected(r.PolicyName, r.RuleName)
	}
	return dec, nil
}

// selectMatching returns the compiled policies whose Match selector
// includes the input and whose Exclude selector does not exclude it.
// The returned slice preserves snapshot ordering (priority desc, name asc).
func (e *Engine) selectMatching(snap *CompiledSnapshot, in Input, act apitypes.Action) []*CompiledPolicy {
	ctx := MatchContext{User: in.User, Tables: in.Tables, Action: act}
	var out []*CompiledPolicy
	for _, p := range snap.Policies {
		if !p.Match.Match(ctx) {
			continue
		}
		// Per-table policy kinds (row filter, column mask) evaluate their
		// Exclude per table in conflict.go so that referencing an excluded
		// table in a multi-table query does not silently drop the policy's
		// protection on the *other* tables. Whole-query kinds (access gate,
		// reject) keep the whole-policy Exclude here.
		if p.Exclude != nil && wholePolicyExclude(p.Kind) && p.Exclude.Match(ctx) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// actionFromInput maps the parsed statement kind to the policy Action verb
// used for resource action scoping. Unknown / unparsed statements yield the
// empty action, which never satisfies an action-constrained selector.
func actionFromInput(in Input) apitypes.Action {
	if in.AST == nil {
		return ""
	}
	switch in.AST.Statement() {
	case parser.StmtSelect:
		return apitypes.ActionSelect
	case parser.StmtInsert:
		return apitypes.ActionInsert
	case parser.StmtUpdate:
		return apitypes.ActionUpdate
	case parser.StmtDelete:
		return apitypes.ActionDelete
	default:
		return ""
	}
}

// wholePolicyExclude reports whether an Exclude selector should drop the
// entire policy when it matches at query scope. True for whole-query kinds
// (SqlAccess gate, QueryReject); false for per-table kinds (RowFilter,
// ColumnMask) whose Exclude is applied per table during conflict
// resolution so a carve-out on one table cannot lift protection on others.
func wholePolicyExclude(k apitypes.Kind) bool {
	switch k {
	case apitypes.KindRowFilterPolicy, apitypes.KindColumnMaskPolicy:
		return false
	default:
		return true
	}
}

// Explain returns an ExplainResult describing which policies match the
// input and, for those that reject, why. Consumed by the admin API and
// the `sluice policy explain` subcommand.
func (e *Engine) Explain(ctx context.Context, in Input) (*apitypes.ExplainResult, error) {
	dec, err := e.Evaluate(ctx, in)
	if err != nil {
		return nil, err
	}
	out := &apitypes.ExplainResult{
		Subject:   subjectLabel(in.User),
		Resource:  resourceLabel(in.Tables),
		Matched:   append([]apitypes.AppliedPolicy(nil), dec.Applied...),
		Shadow:    append([]apitypes.AppliedPolicy(nil), dec.Shadow...),
		Effective: effectiveDecision(dec),
	}
	for _, r := range dec.Rejections {
		out.Rejected = append(out.Rejected, apitypes.RejectedPolicy{
			Kind:   apitypes.KindQueryRejectPolicy,
			Name:   r.PolicyName,
			Reason: r.RuleName + ": " + r.Message,
		})
	}
	if dec.Outcome == OutcomeDeny && dec.DenyReason != nil {
		out.Rejected = append(out.Rejected, apitypes.RejectedPolicy{
			Kind:   apitypes.KindSQLAccessPolicy,
			Name:   dec.DenyReason.PolicyName,
			Reason: dec.DenyReason.Message,
		})
	}
	return out, nil
}

func effectiveDecision(dec *Decision) apitypes.EffectiveDecision {
	out := apitypes.EffectiveDecision{Decision: string(dec.Outcome)}
	for k := range dec.RowFilters {
		out.RowFilters = append(out.RowFilters, k)
	}
	for _, m := range dec.ColumnMasks {
		out.ColumnMasks = append(out.ColumnMasks, apitypes.ColumnMaskRef{
			Column:   m.TableKey + "." + m.Column,
			MaskType: m.Type,
			Policy:   m.Policy,
		})
	}
	return out
}

func subjectLabel(u *identity.UserCtx) string {
	if u == nil {
		return "<anonymous>"
	}
	if u.Subject != "" {
		return u.Subject
	}
	if u.Email != "" {
		return u.Email
	}
	return "<unknown>"
}

func resourceLabel(tables []parser.TableRef) string {
	if len(tables) == 0 {
		return "<none>"
	}
	if len(tables) == 1 {
		return tableKey(tables[0])
	}
	return tableKey(tables[0]) + " (+ " + itoa(len(tables)-1) + " more)"
}

// itoa is a tiny replacement for strconv.Itoa used once in Explain — we
// avoid importing strconv here to keep the cgo-free hot path lean.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
