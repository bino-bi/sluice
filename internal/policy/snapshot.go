// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"context"
	"fmt"
	"sort"

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

	// Kind-specific payloads (one of).
	Access     *CompiledAccess
	RowFilter  *CompiledRowFilter
	ColumnMask *CompiledColumnMask
	Reject     *CompiledReject
	// QueryRewritePolicy is declared-only in MVP. The raw spec is retained
	// so v1 can wire runtime behaviour without reshaping the compiler.
	QueryRewrite *apitypes.RewriteSpec
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

// CompiledRejectRule is a single rule; MVP only emits predeclared rules
// (stored as Name+Message+Code, no evaluator).
type CompiledRejectRule struct {
	Name    string
	Message string
	Code    string
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
	for _, obj := range src.Policies {
		cp, err := compilePolicy(obj)
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
	return out, nil
}

// compilePolicy dispatches on Kind. Non-policy kinds (DataSource,
// SubjectBinding, AuditSink) produce (nil, nil) — the caller filters them
// out.
func compilePolicy(obj apitypes.Object) (*CompiledPolicy, error) {
	meta := obj.GetObjectMeta()
	base := &CompiledPolicy{
		Kind:      obj.GetKind(),
		Name:      meta.Name,
		Namespace: meta.Namespace,
		Priority:  meta.Priority,
	}

	switch p := obj.(type) {
	case *apitypes.SQLAccessPolicy:
		if err := rejectConditions(p.Spec.Conditions); err != nil {
			return nil, err
		}
		if err := compileBase(base, p.Spec.Match, p.Spec.Exclude, p.Spec.EnforcementMode); err != nil {
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
		if err := rejectConditions(p.Spec.Conditions); err != nil {
			return nil, err
		}
		if err := compileBase(base, p.Spec.Match, p.Spec.Exclude, p.Spec.EnforcementMode); err != nil {
			return nil, err
		}
		if p.Spec.Filter.Expression != "" {
			return nil, fmt.Errorf("spec.filter.expression: CEL row filters not supported in MVP")
		}
		if p.Spec.Filter.Predicate == nil {
			return nil, fmt.Errorf("spec.filter.predicate: required")
		}
		pred, err := compilePredicate(p.Spec.Filter.Predicate)
		if err != nil {
			return nil, fmt.Errorf("spec.filter.predicate: %w", err)
		}
		combine := p.Spec.Combine
		if combine == "" {
			combine = apitypes.CombineRestrictive
		}
		base.RowFilter = &CompiledRowFilter{Predicate: pred, Combine: combine}
		return base, nil

	case *apitypes.ColumnMaskPolicy:
		if err := rejectConditions(p.Spec.Conditions); err != nil {
			return nil, err
		}
		if err := compileBase(base, p.Spec.Match, p.Spec.Exclude, p.Spec.EnforcementMode); err != nil {
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
		if err := rejectConditions(p.Spec.Conditions); err != nil {
			return nil, err
		}
		if err := compileBase(base, p.Spec.Match, p.Spec.Exclude, p.Spec.EnforcementMode); err != nil {
			return nil, err
		}
		cr := &CompiledReject{}
		for _, r := range p.Spec.Reject.Rules {
			if r.Expression != "" {
				return nil, fmt.Errorf("%w: rule %q", ErrRejectExprUnsupported, r.Name)
			}
			cr.Rules = append(cr.Rules, CompiledRejectRule{
				Name:    r.Name,
				Message: r.Message,
				Code:    r.Code,
			})
		}
		base.Reject = cr
		return base, nil

	case *apitypes.QueryRewritePolicy:
		if err := rejectConditions(p.Spec.Conditions); err != nil {
			return nil, err
		}
		if err := compileBase(base, p.Spec.Match, p.Spec.Exclude, p.Spec.EnforcementMode); err != nil {
			return nil, err
		}
		spec := p.Spec.Rewrite
		base.QueryRewrite = &spec
		return base, nil
	}

	// DataSource / SubjectBinding / AuditSink: not policies.
	return nil, nil
}

func compileBase(cp *CompiledPolicy, match apitypes.Selector, exclude *apitypes.Selector, enf apitypes.EnforcementMode) error {
	m, err := compileSelector(match)
	if err != nil {
		return fmt.Errorf("spec.match: %w", err)
	}
	cp.Match = m
	if exclude != nil {
		ex, err := compileSelector(*exclude)
		if err != nil {
			return fmt.Errorf("spec.exclude: %w", err)
		}
		cp.Exclude = &ex
	}
	cp.Enforcement = enf
	if cp.Enforcement == "" {
		cp.Enforcement = apitypes.EnforcementEnforce
	}
	if cp.Enforcement != apitypes.EnforcementEnforce {
		return fmt.Errorf("spec.enforcementMode: %q not supported in MVP (use Enforce)", cp.Enforcement)
	}
	return nil
}

// rejectConditions fails compilation if any CEL condition is declared.
// Replaces the CEL evaluator in the MVP; the YAML surface is unchanged so
// policy files written for v1 still parse in MVP when conditions are
// omitted.
func rejectConditions(cs []apitypes.Condition) error {
	for _, c := range cs {
		if c.Expression != "" {
			return fmt.Errorf("%w: condition %q", ErrConditionUnsupported, c.Name)
		}
	}
	return nil
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
	case apitypes.MaskPartial, apitypes.MaskHash,
		apitypes.MaskRegex, apitypes.MaskTruncate,
		apitypes.MaskJitter, apitypes.MaskFPE,
		apitypes.MaskFake, apitypes.MaskCustom, apitypes.MaskExternal:
		// MVP providers are null + constant; other mask types are
		// declared-only until their providers land in pkg/mask.
		return pkgmask.Args{}, fmt.Errorf("mask %q: provider not enabled in MVP", spec.Type)
	default:
		return pkgmask.Args{}, fmt.Errorf("mask type %q: unknown", spec.Type)
	}
	return out, nil
}
