// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"sort"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// resolve consumes the set of policies that matched ctx's selector for
// each table and emits the final Decision. Ordering is deterministic:
//
//  1. Deny override (SqlAccessPolicy effect=deny wins, highest-priority
//     deny supplies the DenyReason).
//  2. Allow required: if no SqlAccessPolicy with effect=allow matched,
//     the request is denied even without an explicit deny.
//  3. Row filters on each table are combined per the policy's Combine
//     mode; restrictive policies AND together, permissive ones OR.
//  4. Masks per column follow priority desc, specificity desc, name asc.
//  5. Reject rules are appended verbatim (one per firing rule).
func resolve(matched []*CompiledPolicy, tables []parser.TableRef, user *identity.UserCtx, action apitypes.Action, firedRejects map[*CompiledPolicy][]CompiledRejectRule) *Decision {
	dec := &Decision{
		Outcome:     OutcomeAllow,
		RowFilters:  map[string]*CompiledFilter{},
		ColumnMasks: map[string]*CompiledMask{},
		Applied:     make([]apitypes.AppliedPolicy, 0, len(matched)),
	}
	// Partition by enforcement mode: only Enforce policies shape the
	// decision; Audit / DryRun policies are recorded as shadow outcomes.
	enforced := make([]*CompiledPolicy, 0, len(matched))
	for _, p := range matched {
		ap := apitypes.AppliedPolicy{Kind: p.Kind, Name: p.Name, Priority: p.Priority}
		if p.Enforcement == apitypes.EnforcementAudit || p.Enforcement == apitypes.EnforcementDryRun {
			dec.Shadow = append(dec.Shadow, ap)
			continue
		}
		enforced = append(enforced, p)
		dec.Applied = append(dec.Applied, ap)
	}

	// Step 1 + 2: access gate.
	if denyOrAllow(enforced, dec); dec.Outcome == OutcomeDeny {
		// Deny short-circuits — downstream steps do not run.
		return dec
	}

	// Step 3: row filters per table.
	collectRowFilters(enforced, tables, user, action, dec)

	// Step 4: column masks per column.
	collectColumnMasks(enforced, tables, user, action, dec)

	// Step 4b: query rewrites (limit / sample / timeout).
	collectRewrites(enforced, dec)

	// Step 5: reject rules that actually fired (expression-gated rules are
	// pre-filtered by the engine; only enforced policies count).
	for _, p := range enforced {
		if p.Kind != apitypes.KindQueryRejectPolicy || p.Reject == nil {
			continue
		}
		rules, ok := firedRejects[p]
		if !ok {
			continue
		}
		for _, r := range rules {
			code := r.Code
			if code == "" {
				code = "ACL_REJECTED"
			}
			dec.Rejections = append(dec.Rejections, Rejection{
				PolicyName: p.Name,
				RuleName:   r.Name,
				Message:    r.Message,
				Code:       code,
			})
		}
	}
	if len(dec.Rejections) > 0 {
		dec.Outcome = OutcomeReject
	}

	return dec
}

// denyOrAllow inspects SqlAccessPolicy outcomes. If any matched policy
// denies, the highest-priority deny wins and dec.Outcome flips to deny.
// Otherwise at least one allow must be present; with none, the request
// is denied by default.
func denyOrAllow(matched []*CompiledPolicy, dec *Decision) {
	var denies, allows []*CompiledPolicy
	for _, p := range matched {
		if p.Kind != apitypes.KindSQLAccessPolicy || p.Access == nil {
			continue
		}
		switch p.Access.Effect {
		case apitypes.EffectDeny:
			denies = append(denies, p)
		case apitypes.EffectAllow:
			allows = append(allows, p)
		}
	}
	if len(denies) > 0 {
		// matched is already priority-desc from Compile; the first deny wins.
		top := denies[0]
		dec.Outcome = OutcomeDeny
		msg := top.Access.Message
		if msg == "" {
			msg = "access denied"
		}
		code := top.Access.ErrorCode
		if code == "" {
			code = "ACL_DENIED"
		}
		dec.DenyReason = &DenyReason{
			PolicyName: top.Name,
			Message:    msg,
			Code:       code,
		}
		return
	}
	if len(allows) == 0 {
		dec.Outcome = OutcomeDeny
		dec.DenyReason = &DenyReason{
			PolicyName: "",
			Message:    "no SqlAccessPolicy matched (default-deny)",
			Code:       "ACL_DENIED",
		}
	}
}

// collectRowFilters walks every matched RowFilterPolicy and folds its
// predicate into dec.RowFilters[tableKey]. Multiple filters on the same
// table are combined per the policy's Combine mode.
func collectRowFilters(matched []*CompiledPolicy, tables []parser.TableRef, user *identity.UserCtx, action apitypes.Action, dec *Decision) {
	for _, p := range matched {
		if p.Kind != apitypes.KindRowFilterPolicy || p.RowFilter == nil {
			continue
		}
		tableRefs := p.Match.MatchingTables(MatchContext{User: user, Tables: tables, Action: action})
		if p.Exclude != nil && len(tableRefs) > 0 {
			kept := tableRefs[:0]
			for _, t := range tableRefs {
				if !p.Exclude.Match(MatchContext{User: user, Tables: []parser.TableRef{t}, Action: action}) {
					kept = append(kept, t)
				}
			}
			tableRefs = kept
		}
		for _, t := range tableRefs {
			key := tableKey(t)
			existing, ok := dec.RowFilters[key]
			if !ok {
				dec.RowFilters[key] = &CompiledFilter{
					TableKey:  key,
					Predicate: p.RowFilter.Predicate,
					Combine:   p.RowFilter.Combine,
					Policies:  []string{p.Name},
				}
				continue
			}
			existing.Predicate = combinePredicates(existing.Predicate, p.RowFilter.Predicate, p.RowFilter.Combine)
			existing.Policies = append(existing.Policies, p.Name)
		}
	}
}

// combinePredicates merges two predicates. Restrictive combine ANDs them
// together; permissive ORs. Nil inputs collapse to the non-nil side.
func combinePredicates(a, b *CompiledPredicate, combine apitypes.Combine) *CompiledPredicate {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if combine == apitypes.CombinePermissive {
		return &CompiledPredicate{Any: []*CompiledPredicate{a, b}}
	}
	return &CompiledPredicate{All: []*CompiledPredicate{a, b}}
}

// collectColumnMasks walks every matched ColumnMaskPolicy and selects,
// per column, the winning mask. Ordering is priority desc, specificity
// desc, name asc — stable and deterministic.
func collectColumnMasks(matched []*CompiledPolicy, tables []parser.TableRef, user *identity.UserCtx, action apitypes.Action, dec *Decision) {
	type candidate struct {
		policy      *CompiledPolicy
		tableKey    string
		table       parser.TableRef
		specificity int
	}
	var cands []candidate
	for _, p := range matched {
		if p.Kind != apitypes.KindColumnMaskPolicy || p.ColumnMask == nil {
			continue
		}
		tableRefs := p.Match.MatchingTables(MatchContext{User: user, Tables: tables, Action: action})
		for _, t := range tableRefs {
			if p.Exclude != nil && p.Exclude.Match(MatchContext{User: user, Tables: []parser.TableRef{t}, Action: action}) {
				continue
			}
			cands = append(cands, candidate{
				policy:      p,
				tableKey:    tableKey(t),
				table:       t,
				specificity: p.Match.Specificity(),
			})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].policy.Priority != cands[j].policy.Priority {
			return cands[i].policy.Priority > cands[j].policy.Priority
		}
		if cands[i].specificity != cands[j].specificity {
			return cands[i].specificity > cands[j].specificity
		}
		return cands[i].policy.Name < cands[j].policy.Name
	})

	// For each candidate, walk the referenced table's columns (via the
	// policy's column selector) and record the winning mask — only the
	// *first* candidate to claim a column wins.
	for _, c := range cands {
		cols := c.policy.Match.MatchingColumns(knownColumns(c.table))
		if len(cols) == 0 {
			// Without a schema cache we cannot enumerate every column; the
			// rewriter performs final resolution (including wildcard
			// expansion) and the engine records the selector pattern for
			// audit. Use the selector's column patterns verbatim so the
			// rewriter can resolve them against the actual columns.
			cols = rawColumns(c.policy)
		}
		for _, col := range cols {
			key := c.tableKey + "." + col
			if _, taken := dec.ColumnMasks[key]; taken {
				continue
			}
			dec.ColumnMasks[key] = &CompiledMask{
				TableKey: c.tableKey,
				Column:   col,
				Type:     c.policy.ColumnMask.Type,
				Args:     c.policy.ColumnMask.Args,
				Policy:   c.policy.Name,
				Priority: c.policy.Priority,
			}
		}
	}
}

// collectRewrites folds every matched QueryRewritePolicy into a single
// RewriteEffect. Limits and timeouts combine in the restrictive direction
// (minimum wins); the sample instruction comes from the first matched
// policy carrying one (matched is priority desc, name asc).
func collectRewrites(matched []*CompiledPolicy, dec *Decision) {
	for _, p := range matched {
		if p.Kind != apitypes.KindQueryRewritePolicy || p.QueryRewrite == nil {
			continue
		}
		if dec.Rewrite == nil {
			dec.Rewrite = &RewriteEffect{}
		}
		eff, r := dec.Rewrite, p.QueryRewrite
		if r.LimitMax > 0 && (eff.LimitMax == 0 || r.LimitMax < eff.LimitMax) {
			eff.LimitMax = r.LimitMax
		}
		if r.Timeout > 0 && (eff.Timeout == 0 || r.Timeout < eff.Timeout) {
			eff.Timeout = r.Timeout
		}
		if r.Sample != nil && eff.Sample == nil {
			eff.Sample = r.Sample
		}
		eff.Policies = append(eff.Policies, p.Name)
	}
}

// knownColumns returns the columns the engine already knows about for t.
// MVP does not push schema.Cache into the engine (cache lives in the
// rewriter layer); return nil so the rawColumns fallback takes over.
func knownColumns(_ parser.TableRef) []string { return nil }

// rawColumns returns the non-wildcard column identifiers declared on the
// policy's Match.resources.columns. Wildcard patterns are returned
// verbatim so the rewriter can expand them against the schema cache.
func rawColumns(p *CompiledPolicy) []string {
	seen := map[string]struct{}{}
	var out []string
	collect := func(clauses []CompiledClause) {
		for _, c := range clauses {
			if c.Resource == nil {
				continue
			}
			for _, m := range c.Resource.columns {
				pat := m.Pattern()
				if _, dup := seen[pat]; dup {
					continue
				}
				seen[pat] = struct{}{}
				out = append(out, pat)
			}
		}
	}
	collect(p.Match.Any)
	collect(p.Match.All)
	return out
}

// tableKey renders a parser.TableRef as "catalog.schema.table". Empty
// catalog stays empty so the rewriter can resolve it against the default
// catalog.
func tableKey(t parser.TableRef) string {
	return t.Catalog + "." + t.Schema + "." + t.Table
}
