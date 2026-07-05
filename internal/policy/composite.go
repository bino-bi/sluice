// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// Composite evaluates several PolicyEngines in order and merges their
// decisions. Semantics:
//
//   - Any member error → the composite errors (fail-closed).
//   - First non-abstained deny → final deny (deny-overrides).
//   - Union of all members' rejections → reject.
//   - Otherwise at least one member must allow; if all abstained → deny.
//   - On allow, grants merge across ALL members (extra restriction is
//     always safe): row filters AND-combine per table (restrictive, never
//     OR'd cross-engine), column masks are first-member-wins (losers go to
//     Shadow), Applied/Shadow concatenate.
type Composite struct {
	members []PolicyEngine
	clock   func() time.Time
	logger  *slog.Logger
}

// NewComposite builds a Composite from ordered members.
func NewComposite(opts Options, members ...PolicyEngine) *Composite {
	c := &Composite{members: members, clock: opts.Clock, logger: opts.Logger}
	if c.clock == nil {
		c.clock = time.Now
	}
	if c.logger == nil {
		c.logger = slog.Default()
	}
	return c
}

// Name reports the composite's members, e.g. "composite(yaml,opa)".
func (c *Composite) Name() string {
	names := make([]string, len(c.members))
	for i, m := range c.members {
		names[i] = m.Name()
	}
	return "composite(" + strings.Join(names, ",") + ")"
}

// ApplySnapshot fans out to every member in order; the first error is
// returned (members that already accepted keep the new snapshot).
func (c *Composite) ApplySnapshot(ctx context.Context, src *config.Snapshot) error {
	for _, m := range c.members {
		if err := m.ApplySnapshot(ctx, src); err != nil {
			return err
		}
	}
	return nil
}

// Evaluate runs every member and merges per the documented semantics.
func (c *Composite) Evaluate(ctx context.Context, in Input) (*Decision, error) {
	start := c.clock()
	decisions := make([]*Decision, 0, len(c.members))
	for _, m := range c.members {
		d, err := m.Evaluate(ctx, in)
		if err != nil {
			return nil, err // fail-closed
		}
		decisions = append(decisions, d)
	}

	// Deny-overrides: first explicit (non-abstained) deny wins.
	for _, d := range decisions {
		if d.Outcome == OutcomeDeny && !d.Abstained {
			d.Duration = c.clock().Sub(start)
			return d, nil
		}
	}

	// Union of rejections.
	merged := &Decision{
		Outcome:     OutcomeAllow,
		RowFilters:  map[string]*CompiledFilter{},
		ColumnMasks: map[string]*CompiledMask{},
	}
	anyAllow := false
	for _, d := range decisions {
		if d.Outcome == OutcomeAllow {
			anyAllow = true
		}
		merged.Rejections = append(merged.Rejections, d.Rejections...)
		merged.Applied = append(merged.Applied, d.Applied...)
		merged.Shadow = append(merged.Shadow, d.Shadow...)
		merged.ApprovalShadow = append(merged.ApprovalShadow, d.ApprovalShadow...)
		merged.Evaluated += d.Evaluated
	}
	if len(merged.Rejections) > 0 {
		merged.Outcome = OutcomeReject
		merged.Duration = c.clock().Sub(start)
		return merged, nil
	}
	if !anyAllow {
		// All members abstained → default-deny at the composite level.
		merged.Outcome = OutcomeDeny
		merged.Abstained = true
		merged.DenyReason = &DenyReason{Message: "no engine allowed the request (default-deny)", Code: "ACL_DENIED"}
		merged.Duration = c.clock().Sub(start)
		return merged, nil
	}

	// Merge grants from ALL members (restriction is always safe).
	for _, d := range decisions {
		for key, f := range d.RowFilters {
			if existing, ok := merged.RowFilters[key]; ok {
				existing.Predicate = combinePredicates(existing.Predicate, f.Predicate, apitypes.CombineRestrictive)
				existing.Policies = append(existing.Policies, f.Policies...)
			} else {
				cp := *f
				cp.Policies = append([]string(nil), f.Policies...)
				merged.RowFilters[key] = &cp
			}
		}
		for key, m := range d.ColumnMasks {
			if _, ok := merged.ColumnMasks[key]; ok {
				merged.Shadow = append(merged.Shadow, apitypes.AppliedPolicy{
					Kind: apitypes.KindColumnMaskPolicy, Name: m.Policy, Priority: m.Priority,
				})
				continue
			}
			merged.ColumnMasks[key] = m
		}
		if d.Rewrite != nil {
			mergeRewrite(merged, d.Rewrite)
		}
		if d.Approval != nil {
			if merged.Approval == nil {
				merged.Approval = &ApprovalRequirement{}
			}
			merged.Approval.Policies = append(merged.Approval.Policies, d.Approval.Policies...)
			merged.Approval.Reasons = append(merged.Approval.Reasons, d.Approval.Reasons...)
		}
	}
	merged.Duration = c.clock().Sub(start)
	return merged, nil
}

// mergeRewrite folds a member's RewriteEffect into the merged decision in
// the restrictive direction.
func mergeRewrite(merged *Decision, r *RewriteEffect) {
	if merged.Rewrite == nil {
		merged.Rewrite = &RewriteEffect{}
	}
	eff := merged.Rewrite
	if r.LimitMax > 0 && (eff.LimitMax == 0 || r.LimitMax < eff.LimitMax) {
		eff.LimitMax = r.LimitMax
	}
	if r.Timeout > 0 && (eff.Timeout == 0 || r.Timeout < eff.Timeout) {
		eff.Timeout = r.Timeout
	}
	if r.Sample != nil && eff.Sample == nil {
		eff.Sample = r.Sample
	}
	eff.Policies = append(eff.Policies, r.Policies...)
}

// Explain concatenates each member's Explain output.
func (c *Composite) Explain(ctx context.Context, in Input) (*apitypes.ExplainResult, error) {
	out := &apitypes.ExplainResult{
		Subject:  subjectLabel(in.User),
		Resource: resourceLabel(in.Tables),
	}
	for _, m := range c.members {
		r, err := m.Explain(ctx, in)
		if err != nil {
			return nil, err
		}
		out.Matched = append(out.Matched, r.Matched...)
		out.Shadow = append(out.Shadow, r.Shadow...)
		out.Rejected = append(out.Rejected, r.Rejected...)
		out.ApprovalRequired = append(out.ApprovalRequired, r.ApprovalRequired...)
	}
	dec, err := c.Evaluate(ctx, in)
	if err != nil {
		return nil, err
	}
	out.Effective = effectiveDecision(dec)
	return out, nil
}

var _ PolicyEngine = (*Composite)(nil)
