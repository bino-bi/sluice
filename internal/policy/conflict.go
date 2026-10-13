// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"sort"
	"strings"

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
		// ApprovalPolicy attribution is owned by evaluateApproval, which
		// only marks a policy Applied when it actually triggers.
		if p.Kind == apitypes.KindApprovalPolicy {
			continue
		}
		ap := apitypes.AppliedPolicy{Kind: p.Kind, Name: p.Name, Priority: p.Priority}
		if p.Enforcement == apitypes.EnforcementAudit || p.Enforcement == apitypes.EnforcementDryRun {
			ap.Would = shadowWould(p, tables, user, action, firedRejects)
			dec.Shadow = append(dec.Shadow, ap)
			continue
		}
		enforced = append(enforced, p)
		dec.Applied = append(dec.Applied, ap)
	}

	// Step 1 + 2: access gate.
	if denyOrAllow(enforced, tables, user, action, dec); dec.Outcome == OutcomeDeny && !dec.Abstained {
		// An EXPLICIT deny short-circuits — downstream steps do not run.
		// An abstained deny (no allow matched) falls through so a composite
		// member's filters/masks can still merge; standalone the outcome
		// stays deny because the reject flip below is guarded on allow.
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
	// Only flip to reject from an allow — an abstained default-deny stays
	// a deny so standalone behaviour is unchanged.
	if len(dec.Rejections) > 0 && dec.Outcome == OutcomeAllow {
		dec.Outcome = OutcomeReject
	}

	return dec
}

// shadowWould names what a policy in Audit or DryRun mode would have done
// to the query in Enforce mode: deny it, or reject it. It is empty for a
// policy that would allow or only shape the query.
func shadowWould(p *CompiledPolicy, tables []parser.TableRef, user *identity.UserCtx, action apitypes.Action, firedRejects map[*CompiledPolicy][]CompiledRejectRule) string {
	switch {
	case p.Kind == apitypes.KindSQLAccessPolicy && p.Access != nil && p.Access.Effect == apitypes.EffectDeny &&
		p.Match.Match(MatchContext{User: user, Tables: notExcluded(p, tables, user, action), Action: action}):
		return "deny"
	case p.Kind == apitypes.KindQueryRejectPolicy && len(firedRejects[p]) > 0:
		return "reject"
	}
	return ""
}

// denyOrAllow inspects SqlAccessPolicy outcomes. If any matched policy
// denies, the highest-priority deny wins and dec.Outcome flips to deny.
// Otherwise every referenced table must be covered by an allow; a table
// that no allow names is denied, so a table joined to an allowed one is
// not admitted by default.
func denyOrAllow(matched []*CompiledPolicy, tables []parser.TableRef, user *identity.UserCtx, action apitypes.Action, dec *Decision) {
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
	// A deny whose Exclude carves out some of the tables fires only if its
	// selector still matches the rest. matched is already priority-desc
	// from Compile; the first deny that fires wins.
	var top *CompiledPolicy
	for _, p := range denies {
		if p.Match.Match(MatchContext{User: user, Tables: notExcluded(p, tables, user, action), Action: action}) {
			top = p
			break
		}
	}
	if top != nil {
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
		dec.Abstained = true // no-opinion deny (composite-mergeable)
		dec.DenyReason = &DenyReason{
			PolicyName: "",
			Message:    "no SqlAccessPolicy matched (default-deny)",
			Code:       "ACL_DENIED",
		}
		return
	}
	// An allow admits only the tables it names. Every referenced table must
	// be covered by some allow, else a sensitive table joined to an allowed
	// one would leak. A statement with no table reference (e.g. SELECT 1)
	// is covered vacuously. Like the no-allow case the deny is an abstain:
	// in a composite an OPA member, which decides on the whole table list,
	// may still allow the statement. A ReBAC member only restricts.
	if t := uncoveredTable(allows, tables, user, action); t != "" {
		dec.Outcome = OutcomeDeny
		dec.Abstained = true
		dec.DenyReason = &DenyReason{
			PolicyName: "",
			Message:    "no SqlAccessPolicy allows " + t,
			Code:       "ACL_DENIED",
		}
	}
}

// notExcluded returns the tables p's Exclude does not carve out. With no
// table reference the statement is kept as is: selectMatching already
// dropped a policy whose Exclude matches it.
func notExcluded(p *CompiledPolicy, tables []parser.TableRef, user *identity.UserCtx, action apitypes.Action) []parser.TableRef {
	// A non-per-table Exclude (an `all` combination) is handled at query
	// scope by selectMatching, not carved here.
	if p.Exclude == nil || !perTableExclude(p.Exclude) {
		return tables
	}
	out := make([]parser.TableRef, 0, len(tables))
	for _, t := range tables {
		if !p.Exclude.Match(MatchContext{User: user, Tables: []parser.TableRef{t}, Action: action}) {
			out = append(out, t)
		}
	}
	return out
}

// uncoveredTable returns the key of the first referenced table that no
// allow policy names, or "". Catalog, schema and table names are matched
// without regard to case by the selectors.
func uncoveredTable(allows []*CompiledPolicy, tables []parser.TableRef, user *identity.UserCtx, action apitypes.Action) string {
	covered := map[string]bool{}
	for _, p := range allows {
		for _, t := range p.Match.MatchingTables(MatchContext{User: user, Tables: notExcluded(p, tables, user, action), Action: action}) {
			covered[tableKey(t)] = true
		}
	}
	for _, t := range tables {
		if !covered[tableKey(t)] {
			return tableKey(t)
		}
	}
	return ""
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
		// A query can reference the same table more than once (self-join,
		// recursive CTE); the policy's predicate applies to the table, not
		// to each reference.
		seen := map[string]bool{}
		for _, t := range tableRefs {
			key := tableKey(t)
			if seen[key] {
				continue
			}
			seen[key] = true
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
			// audit. Use the selector's column patterns verbatim (plus any
			// tag-derived columns for this table) so the rewriter can
			// resolve them against the actual columns.
			cols = rawColumns(c.policy, c.table)
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

// rawColumns returns the column patterns the policy masks on table t: the
// explicit resources.columns patterns plus, for tag-referencing selectors,
// the column patterns of classification rules whose table part matches t.
// Wildcard patterns are returned verbatim so the rewriter can expand them
// against the schema cache.
func rawColumns(p *CompiledPolicy, t parser.TableRef) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(pat string) {
		if _, dup := seen[pat]; dup {
			return
		}
		seen[pat] = struct{}{}
		out = append(out, pat)
	}
	collect := func(clauses []CompiledClause) {
		for _, c := range clauses {
			if c.Resource == nil {
				continue
			}
			for _, m := range c.Resource.columns {
				add(m.Pattern())
			}
			for _, pat := range c.Resource.tagColumnPatterns(t) {
				add(pat)
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
// tableKey is lower case: DuckDB resolves names without regard to case and
// the rewriter looks tables up by this key.
func tableKey(t parser.TableRef) string {
	return strings.ToLower(t.Catalog + "." + t.Schema + "." + t.Table)
}
