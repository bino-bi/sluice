// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgmask "github.com/bino-bi/sluice/pkg/mask"
)

// CompiledSnapshot is the engine's runtime view of the policy set. It is
// immutable once ApplySnapshot accepts it.
type CompiledSnapshot struct {
	Version  int64
	Digest   string
	Policies []*CompiledPolicy
	Bindings []*apitypes.SubjectBinding
	Warnings []string
	// CacheKeyHeaders lists the request header names any policy template
	// references. The rewrite cache includes only these header values in
	// its identity hash so unbounded headers (correlation IDs) don't wreck
	// the hit rate.
	CacheKeyHeaders []string
	// CacheAllHeaders is set when a CEL condition or reject expression is
	// present: those can reference any header opaquely, so the cache must
	// key on the full header set to stay correct.
	CacheAllHeaders bool
}

// CompiledPolicy is the compiled form of an apitypes policy object. Only
// one of Access/RowFilter/ColumnMask/QueryReject/QueryRewrite is
// populated, chosen by Kind.
type CompiledPolicy struct {
	Kind        apitypes.Kind
	Name        string
	Namespace   string
	Priority    int32
	Match       CompiledSelector
	Exclude     *CompiledSelector
	Enforcement apitypes.EnforcementMode
	// Conditions gate whether the policy applies at all: every condition
	// must evaluate true, else the policy is skipped for this request.
	Conditions []CompiledCondition

	// Kind-specific payloads (one of).
	Access       *CompiledAccess
	RowFilter    *CompiledRowFilter
	ColumnMask   *CompiledColumnMask
	Reject       *CompiledReject
	QueryRewrite *CompiledRewrite
	Approval     *CompiledApproval
}

// CompiledApproval is the runtime form of ApprovalPolicy. An empty
// Columns + Predicates means the selector match alone triggers.
type CompiledApproval struct {
	Columns    []apitypes.Matcher
	Predicates []compiledTrigger
	Reason     string
}

// compiledTrigger is a compiled PredicateTrigger.
type compiledTrigger struct {
	Column apitypes.Matcher
	Op     string // "" or "*" = any
	Value  string // "" = any
}

// CompiledAccess is the runtime form of SqlAccessPolicy.
type CompiledAccess struct {
	Effect    apitypes.Effect
	Message   string
	ErrorCode string
}

// CompiledRowFilter captures the compiled predicate plus the combine mode.
type CompiledRowFilter struct {
	Predicate *CompiledPredicate
	Combine   apitypes.Combine
}

// CompiledColumnMask captures the mask type and validated args.
type CompiledColumnMask struct {
	Type       apitypes.MaskType
	Args       pkgmask.Args
	Expression string
}

// CompiledReject is the runtime form of QueryRejectPolicy. The MVP runs
// declared-only: any rule with a non-empty Expression fails ApplySnapshot.
type CompiledReject struct {
	Rules []CompiledRejectRule
}

// CompiledRejectRule is a single rule. When Prog is non-nil the rule only
// fires if the CEL expression evaluates true; a nil Prog fires
// unconditionally (the rule matched by selector alone).
type CompiledRejectRule struct {
	Name    string
	Message string
	Code    string
	Prog    celProgram
}

// CompiledRewrite is the runtime form of QueryRewritePolicy. Every value
// is validated at Compile so the rewriter and queryservice can apply it
// without re-checking.
type CompiledRewrite struct {
	LimitMax int64 // 0 = no limit rewrite; else in [1, math.MaxInt32]
	Sample   *CompiledSample
	Timeout  time.Duration // 0 = no timeout override
}

// CompiledSample is a validated sampling instruction.
type CompiledSample struct {
	Rate   float64 // in (0, 1]
	Method apitypes.SampleMode
}

// Compile lowers a config.Snapshot into a CompiledSnapshot. The returned
// snapshot is ordered by Priority (descending) and policy Name
// (ascending) as a stable tiebreaker so conflict resolution is
// deterministic.
//
// Compile is the slow path: every validation lives here. Evaluate does no
// re-compilation — it only reads the precomputed data.
func Compile(_ context.Context, src *config.Snapshot) (*CompiledSnapshot, error) {
	if src == nil {
		return &CompiledSnapshot{}, nil
	}
	out := &CompiledSnapshot{
		Version:  src.Version,
		Digest:   src.Digest,
		Bindings: append([]*apitypes.SubjectBinding(nil), src.SubjectBindings...),
	}
	tagIndex, err := buildTagIndex(src)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSnapshotInvalid, err)
	}
	for _, obj := range src.Policies {
		cp, err := compilePolicy(obj, tagIndex)
		if err != nil {
			return nil, fmt.Errorf("%w: %s/%s: %w",
				ErrSnapshotInvalid, obj.GetKind(), obj.GetObjectMeta().Name, err)
		}
		if cp == nil {
			continue
		}
		out.Policies = append(out.Policies, cp)
	}
	sort.SliceStable(out.Policies, func(i, j int) bool {
		if out.Policies[i].Priority != out.Policies[j].Priority {
			return out.Policies[i].Priority > out.Policies[j].Priority
		}
		return out.Policies[i].Name < out.Policies[j].Name
	})
	out.CacheKeyHeaders, out.CacheAllHeaders = collectCacheHeaders(out.Policies)
	return out, nil
}

// buildTagIndex compiles every DataClassification rule into a tag →
// classification-resource index used to expand ResourceSelector.Tags at
// compile time.
func buildTagIndex(src *config.Snapshot) (map[string][]*compiledResource, error) {
	objs := src.ByKind[apitypes.KindDataClassification]
	if len(objs) == 0 {
		return nil, nil
	}
	index := map[string][]*compiledResource{}
	for _, obj := range objs {
		dc, ok := obj.(*apitypes.DataClassification)
		if !ok {
			continue
		}
		for i, rule := range dc.Spec.Rules {
			// Classification rules cannot themselves reference tags (nil
			// index) — validated in apitypes, belt-and-braces here.
			cr, err := compileResourceSelector(rule.Resources, nil)
			if err != nil {
				return nil, fmt.Errorf("DataClassification %s: rule %d: %w", dc.Metadata.Name, i, err)
			}
			for _, tag := range rule.Tags {
				index[tag] = append(index[tag], cr)
			}
		}
	}
	return index, nil
}

// collectCacheHeaders gathers the request header names referenced by
// policy templates and reports whether any CEL condition/reject makes the
// header set opaque (forcing a full-header cache key).
func collectCacheHeaders(policies []*CompiledPolicy) ([]string, bool) {
	seen := map[string]struct{}{}
	allHeaders := false
	addFromPredicate := func(p *CompiledPredicate) {
		if p == nil {
			return
		}
		for _, tmpl := range predicateHeaderTemplates(p) {
			seen[tmpl] = struct{}{}
		}
	}
	for _, p := range policies {
		if len(p.Conditions) > 0 {
			allHeaders = true
		}
		if p.RowFilter != nil {
			addFromPredicate(p.RowFilter.Predicate)
		}
		if p.Reject != nil {
			for _, r := range p.Reject.Rules {
				if r.Prog != nil {
					allHeaders = true
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out, allHeaders
}

// predicateHeaderTemplates returns the header names referenced by
// request.headers.<name> templates anywhere in the predicate tree.
func predicateHeaderTemplates(p *CompiledPredicate) []string {
	if p == nil {
		return nil
	}
	var out []string
	for _, c := range p.All {
		out = append(out, predicateHeaderTemplates(c)...)
	}
	for _, c := range p.Any {
		out = append(out, predicateHeaderTemplates(c)...)
	}
	out = append(out, predicateHeaderTemplates(p.Not)...)
	for _, v := range p.Values {
		if v.Template != nil && len(v.Template.Path) == 3 &&
			v.Template.Path[0] == "request" && v.Template.Path[1] == "headers" {
			out = append(out, v.Template.Path[2])
		}
	}
	return out
}

// compilePolicy dispatches on Kind. Non-policy kinds (DataSource,
// SubjectBinding, AuditSink) produce (nil, nil) — the caller filters them
// out.
func compilePolicy(obj apitypes.Object, tagIndex map[string][]*compiledResource) (*CompiledPolicy, error) {
	meta := obj.GetObjectMeta()
	base := &CompiledPolicy{
		Kind:      obj.GetKind(),
		Name:      meta.Name,
		Namespace: meta.Namespace,
		Priority:  meta.Priority,
	}

	switch p := obj.(type) {
	case *apitypes.SQLAccessPolicy:
		if err := compileBase(base, p.Spec.Match, p.Spec.Exclude, p.Spec.EnforcementMode, p.Spec.Conditions, tagIndex); err != nil {
			return nil, err
		}
		if p.Spec.Effect != apitypes.EffectAllow && p.Spec.Effect != apitypes.EffectDeny {
			return nil, fmt.Errorf("spec.effect: must be allow or deny, got %q", p.Spec.Effect)
		}
		base.Access = &CompiledAccess{
			Effect:    p.Spec.Effect,
			Message:   p.Spec.Message,
			ErrorCode: p.Spec.ErrorCode,
		}
		return base, nil

	case *apitypes.RowFilterPolicy:
		if err := compileBase(base, p.Spec.Match, p.Spec.Exclude, p.Spec.EnforcementMode, p.Spec.Conditions, tagIndex); err != nil {
			return nil, err
		}
		combine := p.Spec.Combine
		if combine == "" {
			combine = apitypes.CombineRestrictive
		}
		if p.Spec.Filter.Expression != "" && p.Spec.Filter.Predicate != nil {
			return nil, fmt.Errorf("spec.filter: set exactly one of predicate or expression")
		}
		var pred *CompiledPredicate
		switch {
		case p.Spec.Filter.Expression != "":
			var err error
			pred, err = compileFilterExpression(p.Spec.Filter.Expression)
			if err != nil {
				return nil, fmt.Errorf("spec.filter.expression: %w", err)
			}
		case p.Spec.Filter.Predicate != nil:
			var err error
			pred, err = compilePredicate(p.Spec.Filter.Predicate)
			if err != nil {
				return nil, fmt.Errorf("spec.filter.predicate: %w", err)
			}
		default:
			return nil, fmt.Errorf("spec.filter: predicate or expression required")
		}
		base.RowFilter = &CompiledRowFilter{Predicate: pred, Combine: combine}
		return base, nil

	case *apitypes.ColumnMaskPolicy:
		if err := compileBase(base, p.Spec.Match, p.Spec.Exclude, p.Spec.EnforcementMode, p.Spec.Conditions, tagIndex); err != nil {
			return nil, err
		}
		args, err := compileMaskArgs(p.Spec.Mask)
		if err != nil {
			return nil, fmt.Errorf("spec.mask: %w", err)
		}
		base.ColumnMask = &CompiledColumnMask{
			Type:       p.Spec.Mask.Type,
			Args:       args,
			Expression: p.Spec.Mask.Expression,
		}
		return base, nil

	case *apitypes.QueryRejectPolicy:
		if err := compileBase(base, p.Spec.Match, p.Spec.Exclude, p.Spec.EnforcementMode, p.Spec.Conditions, tagIndex); err != nil {
			return nil, err
		}
		cr := &CompiledReject{}
		for _, r := range p.Spec.Reject.Rules {
			rule := CompiledRejectRule{Name: r.Name, Message: r.Message, Code: r.Code}
			if r.Expression != "" {
				e, err := env()
				if err != nil {
					return nil, fmt.Errorf("cel env: %w", err)
				}
				prog, err := compileBoolProgram(e, r.Expression)
				if err != nil {
					return nil, fmt.Errorf("reject rule %q: %w", r.Name, err)
				}
				rule.Prog = prog
			}
			cr.Rules = append(cr.Rules, rule)
		}
		base.Reject = cr
		return base, nil

	case *apitypes.QueryRewritePolicy:
		if err := compileBase(base, p.Spec.Match, p.Spec.Exclude, p.Spec.EnforcementMode, p.Spec.Conditions, tagIndex); err != nil {
			return nil, err
		}
		cr, err := compileRewrite(p.Spec.Rewrite)
		if err != nil {
			return nil, err
		}
		base.QueryRewrite = cr
		return base, nil

	case *apitypes.ApprovalPolicy:
		if err := compileBase(base, p.Spec.Match, p.Spec.Exclude, p.Spec.EnforcementMode, p.Spec.Conditions, tagIndex); err != nil {
			return nil, err
		}
		ca, err := compileApproval(p.Spec)
		if err != nil {
			return nil, err
		}
		base.Approval = ca
		return base, nil
	}

	// DataSource / SubjectBinding / AuditSink: not policies.
	return nil, nil
}

func compileBase(cp *CompiledPolicy, match apitypes.Selector, exclude *apitypes.Selector, enf apitypes.EnforcementMode, conditions []apitypes.Condition, tagIndex map[string][]*compiledResource) error {
	m, err := compileSelectorWithTags(match, tagIndex)
	if err != nil {
		return fmt.Errorf("spec.match: %w", err)
	}
	cp.Match = m
	if exclude != nil {
		ex, err := compileSelectorWithTags(*exclude, tagIndex)
		if err != nil {
			return fmt.Errorf("spec.exclude: %w", err)
		}
		cp.Exclude = &ex
	}
	conds, err := compileConditions(conditions)
	if err != nil {
		return fmt.Errorf("spec.conditions: %w", err)
	}
	cp.Conditions = conds
	cp.Enforcement = enf
	if cp.Enforcement == "" {
		cp.Enforcement = apitypes.EnforcementEnforce
	}
	switch cp.Enforcement {
	case apitypes.EnforcementEnforce, apitypes.EnforcementAudit, apitypes.EnforcementDryRun:
		// Enforce shapes the decision; Audit/DryRun match and are recorded
		// as shadow outcomes but do not affect enforcement.
	default:
		return fmt.Errorf("spec.enforcementMode: %q invalid (use Enforce, Audit, or DryRun)", cp.Enforcement)
	}
	return nil
}

// compileApproval validates an ApprovalSpec into its runtime form.
func compileApproval(spec apitypes.ApprovalSpec) (*CompiledApproval, error) {
	out := &CompiledApproval{Reason: spec.Reason}
	if spec.When == nil {
		return out, nil
	}
	for _, pat := range spec.When.ColumnsAccessed {
		m, err := apitypes.CompileWildcard(pat)
		if err != nil {
			return nil, fmt.Errorf("spec.when.columnsAccessed %q: %w", pat, err)
		}
		out.Columns = append(out.Columns, m)
	}
	for i, tr := range spec.When.Predicates {
		if tr.Column == "" {
			return nil, fmt.Errorf("spec.when.predicates[%d]: column is required", i)
		}
		m, err := apitypes.CompileWildcard(tr.Column)
		if err != nil {
			return nil, fmt.Errorf("spec.when.predicates[%d].column %q: %w", i, tr.Column, err)
		}
		out.Predicates = append(out.Predicates, compiledTrigger{Column: m, Op: tr.Op, Value: tr.Value})
	}
	return out, nil
}

// compileRewrite validates a RewriteSpec into its runtime form. Hints are
// rejected: nothing consumes them, and silently accepting an inert rewrite
// instruction would misrepresent the enforced posture.
func compileRewrite(spec apitypes.RewriteSpec) (*CompiledRewrite, error) {
	if len(spec.Hints) > 0 {
		return nil, fmt.Errorf("spec.rewrite.hint: hints are not supported")
	}
	out := &CompiledRewrite{}
	if spec.Limit != nil {
		if spec.Limit.Max < 1 || spec.Limit.Max > math.MaxInt32 {
			return nil, fmt.Errorf("spec.rewrite.limit.max: must be in [1, %d], got %d", math.MaxInt32, spec.Limit.Max)
		}
		out.LimitMax = spec.Limit.Max
	}
	if spec.Sample != nil {
		if spec.Sample.Rate <= 0 || spec.Sample.Rate > 1 {
			return nil, fmt.Errorf("spec.rewrite.sample.rate: must be in (0, 1], got %v", spec.Sample.Rate)
		}
		method := spec.Sample.Method
		if method == "" {
			method = apitypes.SampleReservoir
		}
		switch method {
		case apitypes.SampleReservoir, apitypes.SampleBernoulli, apitypes.SampleSystem:
		default:
			return nil, fmt.Errorf("spec.rewrite.sample.method: %q invalid (use reservoir, bernoulli, or system)", method)
		}
		out.Sample = &CompiledSample{Rate: spec.Sample.Rate, Method: method}
	}
	if d := time.Duration(spec.Timeout); d != 0 {
		if d < 0 {
			return nil, fmt.Errorf("spec.rewrite.timeout: must be positive, got %v", d)
		}
		out.Timeout = d
	}
	if out.LimitMax == 0 && out.Sample == nil && out.Timeout == 0 {
		return nil, fmt.Errorf("spec.rewrite: at least one of limit, sample, or timeout is required")
	}
	return out, nil
}

// compileMaskArgs validates and mirrors apitypes.MaskArgs into pkgmask.Args.
func compileMaskArgs(spec apitypes.MaskSpec) (pkgmask.Args, error) {
	src := spec.Args
	out := pkgmask.Args{
		Value:          src.Value,
		ShowFirst:      src.ShowFirst,
		ShowLast:       src.ShowLast,
		MaskChar:       src.MaskChar,
		Algorithm:      string(src.Algorithm),
		SaltRef:        src.SaltRef,
		Pattern:        src.Pattern,
		Replacement:    src.Replacement,
		Length:         src.Length,
		Suffix:         src.Suffix,
		Range:          src.Range,
		Seed:           src.Seed,
		KeyRef:         src.KeyRef,
		Tweak:          src.Tweak,
		Alphabet:       src.Alphabet,
		CustomAlphabet: src.CustomAlphabet,
		FakeType:       src.FakeType,
		Provider:       src.Provider,
		KeyPath:        src.KeyPath,
		Expression:     spec.Expression,
		Extras:         src.Extras,
	}

	switch spec.Type {
	case apitypes.MaskNull:
		// No args required.
	case apitypes.MaskConstant:
		if src.Value == nil {
			return pkgmask.Args{}, fmt.Errorf("mask constant: value required")
		}
	case apitypes.MaskCustom, apitypes.MaskExternal:
		// External / custom mask types are declared-only until their
		// providers land in pkg/mask.
		return pkgmask.Args{}, fmt.Errorf("mask %q: provider not enabled", spec.Type)
	case apitypes.MaskPartial, apitypes.MaskHash,
		apitypes.MaskRegex, apitypes.MaskTruncate,
		apitypes.MaskJitter, apitypes.MaskFPE, apitypes.MaskFake:
		// Registry-driven validation: the provider that renders the SQL
		// at rewrite time is the same one that vets the args at load
		// time, so `policy validate` and the runtime can never disagree.
		provider, ok := pkgmask.Default().Lookup(string(spec.Type))
		if !ok {
			return pkgmask.Args{}, fmt.Errorf("mask %q: provider not registered", spec.Type)
		}
		if err := provider.ValidateArgs(out); err != nil {
			return pkgmask.Args{}, fmt.Errorf("mask %q: %w", spec.Type, err)
		}
	default:
		return pkgmask.Args{}, fmt.Errorf("mask type %q: unknown", spec.Type)
	}
	return out, nil
}
