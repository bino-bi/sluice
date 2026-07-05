// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// evaluateApproval runs after resolve() on an allow outcome. It folds
// every triggered ApprovalPolicy into dec.Approval (Enforce mode) or
// dec.ApprovalShadow (Audit/DryRun). Precedence is structural: deny
// short-circuits in resolve(), rejections flip the outcome to reject, and
// this only runs on OutcomeAllow — so deny > reject > approval holds.
func evaluateApproval(matched []*CompiledPolicy, in Input, dec *Decision) {
	for _, p := range matched {
		if p.Kind != apitypes.KindApprovalPolicy || p.Approval == nil {
			continue
		}
		if !approvalTriggers(p.Approval, in) {
			continue
		}
		ap := apitypes.AppliedPolicy{Kind: p.Kind, Name: p.Name, Priority: p.Priority}
		reason := p.Approval.Reason
		if reason == "" {
			reason = "matched approval policy " + p.Name
		}
		if p.Enforcement == apitypes.EnforcementAudit || p.Enforcement == apitypes.EnforcementDryRun {
			dec.ApprovalShadow = append(dec.ApprovalShadow, ap)
			dec.Shadow = append(dec.Shadow, ap)
			continue
		}
		if dec.Approval == nil {
			dec.Approval = &ApprovalRequirement{}
		}
		dec.Approval.Policies = append(dec.Approval.Policies, ap)
		dec.Approval.Reasons = append(dec.Approval.Reasons, "policy "+p.Name+": "+reason)
		dec.Applied = append(dec.Applied, ap)
	}
}

// approvalTriggers reports whether an ApprovalPolicy's dynamic conditions
// fire for this request. An empty When triggers on selector match alone.
// When the AST is unavailable (regex fallback) a policy with a non-empty
// When is treated as triggered — fail-closed, since we cannot inspect the
// columns or comparisons.
func approvalTriggers(ca *CompiledApproval, in Input) bool {
	if len(ca.Columns) == 0 && len(ca.Predicates) == 0 {
		return true
	}
	if in.AST == nil {
		return true
	}
	shape := in.Shape
	for _, m := range ca.Columns {
		for _, col := range shape.AccessedColumns {
			if matchColumn(m, col) {
				return true
			}
		}
	}
	for _, tr := range ca.Predicates {
		for _, cmp := range shape.Comparisons {
			if triggerMatches(tr, cmp) {
				return true
			}
		}
	}
	return false
}

// matchColumn matches a wildcard against a column reference by both its
// full dotted form and its bare last segment (so `email` matches
// `u.email`).
func matchColumn(m apitypes.Matcher, col string) bool {
	if m.Match(col) {
		return true
	}
	if bare := lastSegment(col); bare != col {
		return m.Match(bare)
	}
	return false
}

func triggerMatches(tr compiledTrigger, cmp parser.Comparison) bool {
	if !matchColumn(tr.Column, cmp.Column) {
		return false
	}
	if tr.Op != "" && tr.Op != "*" && tr.Op != cmp.Op {
		return false
	}
	if tr.Value != "" && tr.Value != cmp.Value {
		return false
	}
	return true
}

func lastSegment(dotted string) string {
	for i := len(dotted) - 1; i >= 0; i-- {
		if dotted[i] == '.' {
			return dotted[i+1:]
		}
	}
	return dotted
}
