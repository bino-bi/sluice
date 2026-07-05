// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// CompiledSelector is the compiled form of an apitypes.Selector.
type CompiledSelector struct {
	Any []CompiledClause
	All []CompiledClause
}

// CompiledClause is one (subject-match, resource-match) pair.
type CompiledClause struct {
	Subject  *compiledSubject // nil means "matches everything"
	Resource *compiledResource
}

// compiledSubject is the compiled SubjectSelector. All sub-checks must
// pass; an empty field imposes no constraint.
type compiledSubject struct {
	groups    []string
	apiKeyIDs []string
	ipNets    []*net.IPNet
	roles     []string
	claims    []compiledClaimCheck
	empty     bool
}

// compiledClaimCheck is a precompiled ClaimCheck; Matches resolves a
// regex lazily so callers see the error only when the claim is actually
// evaluated.
type compiledClaimCheck struct {
	path    []string
	op      apitypes.ClaimOp
	value   any
	values  []any
	pattern *regexp.Regexp
	raw     string
}

// compiledResource is the compiled ResourceSelector. Catalogs, Schemas,
// Tables, and Columns use the apitypes wildcard grammar. Actions are
// matched as a set (empty set = match any action).
type compiledResource struct {
	catalogs []apitypes.Matcher
	schemas  []apitypes.Matcher
	tables   []apitypes.Matcher
	columns  []apitypes.Matcher
	actions  map[apitypes.Action]struct{}
	// specificity = sum of non-wildcard segments; used as a tiebreaker
	// in conflict resolution (higher = more specific).
	specificity int
	empty       bool
}

// MatchContext is the input to a CompiledClause match attempt.
type MatchContext struct {
	User   *identity.UserCtx
	Tables []parser.TableRef
	// Action is the SQL verb of the statement (SELECT/INSERT/…). Empty when
	// the verb is unknown (e.g. regex fallback). A resource selector that
	// constrains actions matches only when Action is one of them.
	Action apitypes.Action
}

// compileSelector lowers an apitypes.Selector into a CompiledSelector. A
// nil selector is allowed and compiles to the zero value (no clauses).
func compileSelector(sel apitypes.Selector) (CompiledSelector, error) {
	out := CompiledSelector{}
	for _, c := range sel.Any {
		cc, err := compileClause(c)
		if err != nil {
			return CompiledSelector{}, err
		}
		out.Any = append(out.Any, cc)
	}
	for _, c := range sel.All {
		cc, err := compileClause(c)
		if err != nil {
			return CompiledSelector{}, err
		}
		out.All = append(out.All, cc)
	}
	return out, nil
}

func compileClause(c apitypes.Clause) (CompiledClause, error) {
	out := CompiledClause{}
	if c.Subjects != nil {
		s, err := compileSubjectSelector(*c.Subjects)
		if err != nil {
			return CompiledClause{}, err
		}
		out.Subject = s
	}
	if c.Resources != nil {
		r, err := compileResourceSelector(*c.Resources)
		if err != nil {
			return CompiledClause{}, err
		}
		out.Resource = r
	}
	return out, nil
}

func compileSubjectSelector(s apitypes.SubjectSelector) (*compiledSubject, error) {
	out := &compiledSubject{
		groups:    append([]string(nil), s.Groups...),
		apiKeyIDs: append([]string(nil), s.APIKeys...),
		roles:     append([]string(nil), s.Roles...),
	}
	for _, cidr := range s.IPRanges {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("subject.ipRanges: %w", err)
		}
		out.ipNets = append(out.ipNets, n)
	}
	for _, cc := range s.JWTClaims {
		cmp, err := compileClaimCheck(cc)
		if err != nil {
			return nil, err
		}
		out.claims = append(out.claims, cmp)
	}
	out.empty = len(out.groups) == 0 && len(out.apiKeyIDs) == 0 &&
		len(out.roles) == 0 && len(out.ipNets) == 0 && len(out.claims) == 0
	return out, nil
}

func compileClaimCheck(cc apitypes.ClaimCheck) (compiledClaimCheck, error) {
	if strings.TrimSpace(cc.Claim) == "" {
		return compiledClaimCheck{}, fmt.Errorf("claim check: claim path required")
	}
	path := strings.Split(cc.Claim, ".")
	out := compiledClaimCheck{path: path, op: cc.Op, value: cc.Value, values: cc.Values, raw: cc.Claim}
	if cc.Op == apitypes.ClaimOpMatches {
		if cc.Pattern == "" {
			return compiledClaimCheck{}, fmt.Errorf("claim %q Matches: pattern required", cc.Claim)
		}
		re, err := regexp.Compile(cc.Pattern)
		if err != nil {
			return compiledClaimCheck{}, fmt.Errorf("claim %q Matches: %w", cc.Claim, err)
		}
		out.pattern = re
	}
	return out, nil
}

func compileResourceSelector(r apitypes.ResourceSelector) (*compiledResource, error) {
	out := &compiledResource{}
	var err error
	if out.catalogs, err = compileMatchers(r.Catalogs, "catalogs"); err != nil {
		return nil, err
	}
	if out.schemas, err = compileMatchers(r.Schemas, "schemas"); err != nil {
		return nil, err
	}
	if out.tables, err = compileMatchers(r.Tables, "tables"); err != nil {
		return nil, err
	}
	if out.columns, err = compileMatchers(r.Columns, "columns"); err != nil {
		return nil, err
	}
	if len(r.Actions) > 0 {
		out.actions = make(map[apitypes.Action]struct{}, len(r.Actions))
		for _, a := range r.Actions {
			out.actions[a] = struct{}{}
		}
	}
	out.specificity = countStatic(r.Catalogs) + countStatic(r.Schemas) +
		countStatic(r.Tables) + countStatic(r.Columns)
	out.empty = len(out.catalogs) == 0 && len(out.schemas) == 0 &&
		len(out.tables) == 0 && len(out.columns) == 0 && len(out.actions) == 0
	return out, nil
}

func compileMatchers(patterns []string, field string) ([]apitypes.Matcher, error) {
	out := make([]apitypes.Matcher, 0, len(patterns))
	for _, p := range patterns {
		m, err := apitypes.CompileWildcard(p)
		if err != nil {
			return nil, fmt.Errorf("resource.%s: %w", field, err)
		}
		out = append(out, m)
	}
	return out, nil
}

// countStatic returns the number of patterns in patterns that contain no
// wildcard characters. Higher values mean a more specific selector.
func countStatic(patterns []string) int {
	n := 0
	for _, p := range patterns {
		if !strings.ContainsAny(p, "*") {
			n++
		}
	}
	return n
}

// Match reports whether ctx satisfies the selector. An entirely empty
// selector matches nothing (explicit default-deny); a selector with any
// populated clause matches when that clause matches.
func (s *CompiledSelector) Match(ctx MatchContext) bool {
	if s == nil {
		return false
	}
	if len(s.Any) == 0 && len(s.All) == 0 {
		return false
	}
	for _, c := range s.Any {
		if c.Match(ctx) {
			return true
		}
	}
	if len(s.All) == 0 {
		return false
	}
	for _, c := range s.All {
		if !c.Match(ctx) {
			return false
		}
	}
	return true
}

// MatchingTables returns the subset of tables for which any clause in the
// selector matches. Used by row-filter / column-mask evaluation to
// identify *which* tables a policy applies to.
func (s *CompiledSelector) MatchingTables(ctx MatchContext) []parser.TableRef {
	if s == nil {
		return nil
	}
	var out []parser.TableRef
	for _, t := range ctx.Tables {
		single := MatchContext{User: ctx.User, Tables: []parser.TableRef{t}, Action: ctx.Action}
		if s.Match(single) {
			out = append(out, t)
		}
	}
	return out
}

// MatchingColumns returns the subset of candidate columns for which the
// selector's column matcher (if any) matches. Used by ColumnMaskPolicy.
// All returned columns implicitly belong to a table that already matched.
func (s *CompiledSelector) MatchingColumns(candidates []string) []string {
	if s == nil {
		return nil
	}
	// Collect resource.columns matchers across all clauses.
	var matchers []apitypes.Matcher
	for _, c := range s.Any {
		if c.Resource != nil {
			matchers = append(matchers, c.Resource.columns...)
		}
	}
	for _, c := range s.All {
		if c.Resource != nil {
			matchers = append(matchers, c.Resource.columns...)
		}
	}
	if len(matchers) == 0 {
		// No column selector → match nothing (column masks require an
		// explicit columns list — otherwise every column in the table
		// would be silently masked).
		return nil
	}
	out := make([]string, 0, len(candidates))
	for _, col := range candidates {
		for _, m := range matchers {
			if m.Match(col) {
				out = append(out, col)
				break
			}
		}
	}
	return out
}

// Specificity returns the highest specificity score across the clauses.
// The value is used as a deterministic tiebreaker during mask conflict
// resolution.
func (s *CompiledSelector) Specificity() int {
	if s == nil {
		return 0
	}
	best := 0
	for _, c := range s.Any {
		if c.Resource != nil && c.Resource.specificity > best {
			best = c.Resource.specificity
		}
	}
	for _, c := range s.All {
		if c.Resource != nil && c.Resource.specificity > best {
			best = c.Resource.specificity
		}
	}
	return best
}

// Match evaluates one clause. An empty subject matches every subject; an
// empty resource matches every resource.
func (c CompiledClause) Match(ctx MatchContext) bool {
	if c.Subject != nil && !c.Subject.empty {
		if !c.Subject.matches(ctx.User) {
			return false
		}
	}
	if c.Resource == nil || c.Resource.empty {
		return true
	}
	// Action scoping is query-global: when the resource constrains actions,
	// the statement's verb must be one of them. An unknown verb never
	// satisfies an action constraint (fail-closed).
	if len(c.Resource.actions) > 0 {
		if ctx.Action == "" {
			return false
		}
		if _, ok := c.Resource.actions[ctx.Action]; !ok {
			return false
		}
	}
	// A resource that constrains only actions (no catalog/schema/table
	// matchers) matches every table once the action check has passed.
	if len(c.Resource.catalogs) == 0 && len(c.Resource.schemas) == 0 && len(c.Resource.tables) == 0 {
		return true
	}
	for _, tref := range ctx.Tables {
		if c.Resource.matches(tref) {
			return true
		}
	}
	return false
}

func (s *compiledSubject) matches(u *identity.UserCtx) bool {
	if s == nil || s.empty {
		return true
	}
	if u == nil {
		return false
	}
	if len(s.groups) > 0 {
		if !anyMember(s.groups, u.Groups) {
			return false
		}
	}
	if len(s.apiKeyIDs) > 0 {
		if u.AuthMethod != identity.AuthMethodAPIKey || !contains(s.apiKeyIDs, u.Subject) {
			return false
		}
	}
	if len(s.roles) > 0 {
		// Roles are expected to live in the "roles" claim as a string
		// slice. Fall back to matching against groups so simple
		// deployments that put roles in groups still work.
		rolesAny, ok := u.Claims["roles"]
		if !ok {
			if !anyMember(s.roles, u.Groups) {
				return false
			}
		} else {
			rolesList := toStringSlice(rolesAny)
			if !anyMember(s.roles, rolesList) {
				return false
			}
		}
	}
	if len(s.ipNets) > 0 {
		// Matching by remote IP is deferred: IdPs typically populate
		// the RemoteAddr on UserCtx via middleware. An empty address
		// fails — no IP means no IP-range match.
		ip := net.ParseIP(u.RemoteAddr)
		if ip == nil {
			return false
		}
		ok := false
		for _, n := range s.ipNets {
			if n.Contains(ip) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for _, cc := range s.claims {
		if !cc.matches(u.Claims) {
			return false
		}
	}
	return true
}

func (cc compiledClaimCheck) matches(claims map[string]any) bool {
	val, found := walkClaimPath(claims, cc.path)
	switch cc.op {
	case apitypes.ClaimOpExists:
		return found
	case apitypes.ClaimOpEquals:
		return found && deepEqual(val, cc.value)
	case apitypes.ClaimOpNotEquals:
		return !found || !deepEqual(val, cc.value)
	case apitypes.ClaimOpIn:
		if !found {
			return false
		}
		for _, v := range cc.values {
			if deepEqual(val, v) {
				return true
			}
		}
		return false
	case apitypes.ClaimOpNotIn:
		if !found {
			return true
		}
		for _, v := range cc.values {
			if deepEqual(val, v) {
				return false
			}
		}
		return true
	case apitypes.ClaimOpMatches:
		if !found || cc.pattern == nil {
			return false
		}
		s, ok := val.(string)
		if !ok {
			return false
		}
		return cc.pattern.MatchString(s)
	}
	return false
}

// walkClaimPath descends through nested map[string]any values by path
// segments. Returns (value, true) on hit.
func walkClaimPath(claims map[string]any, path []string) (any, bool) {
	var current any = claims
	for _, seg := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, found := m[seg]
		if !found {
			return nil, false
		}
		current = next
	}
	return current, true
}

// matches reports whether a single TableRef satisfies the resource
// selector. An empty Catalog on the ref is treated as "any catalog" so
// unqualified SQL still matches catalog-scoped policies.
func (r *compiledResource) matches(t parser.TableRef) bool {
	if r == nil || r.empty {
		return true
	}
	if len(r.catalogs) > 0 && !matchAny(r.catalogs, t.Catalog) {
		return false
	}
	if len(r.schemas) > 0 && !matchAny(r.schemas, t.Schema) {
		return false
	}
	if len(r.tables) > 0 && !matchAny(r.tables, t.Table) {
		return false
	}
	// Columns and actions are evaluated above the selector in the engine;
	// at this level they do not constrain table matching.
	return true
}

func matchAny(ms []apitypes.Matcher, s string) bool {
	// Permit empty input when the only matcher is "**" (match-everything).
	for _, m := range ms {
		if m.Match(s) {
			return true
		}
	}
	return false
}

func anyMember(wants, haves []string) bool {
	for _, h := range haves {
		for _, w := range wants {
			if h == w {
				return true
			}
		}
	}
	return false
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func toStringSlice(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{x}
	}
	return nil
}

// deepEqual compares two claim values from JSON/YAML decoding. Exact
// type+value matches win first. Numbers may arrive as float64 (JSON) or int
// (YAML), so numeric values are compared numerically. Crucially there is no
// stringify fallback: a numeric claim never equals a string policy value
// (e.g. 1 != "1", true != "true"), which prevents type-confused matches in
// security-sensitive claim gating.
func deepEqual(a, b any) bool {
	if a == b {
		return true
	}
	if af, aok := numericValue(a); aok {
		if bf, bok := numericValue(b); bok {
			return af == bf
		}
	}
	return false
}

// numericValue coerces the integer/float shapes JSON and YAML decoders
// produce into a float64 for numeric comparison.
func numericValue(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
