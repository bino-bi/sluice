// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"fmt"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// CompiledPredicate is a boolean tree suitable for the rewriter to emit as
// a parameterised WHERE fragment. Leaves carry a column reference, an
// operator, and zero or more value sources (literals or templates).
type CompiledPredicate struct {
	// Internal node variants.
	All []*CompiledPredicate
	Any []*CompiledPredicate
	Not *CompiledPredicate

	// Leaf fields (populated when All/Any/Not are all empty).
	Column string
	Op     apitypes.PredOp
	Values []ValueSource // positional arguments for Op
}

// IsLeaf reports whether p carries a column + op + value set.
func (p *CompiledPredicate) IsLeaf() bool {
	if p == nil {
		return false
	}
	return len(p.All) == 0 && len(p.Any) == 0 && p.Not == nil && p.Column != ""
}

// ValueSource describes one argument to a predicate leaf. Exactly one of
// Literal or Template is set; both false means an IsNull / IsNotNull
// predicate that takes no value.
type ValueSource struct {
	Literal  any
	Template *Template // non-nil means the value is rendered per request
}

// HasTemplate reports whether any template appears anywhere in the tree.
func (p *CompiledPredicate) HasTemplate() bool {
	if p == nil {
		return false
	}
	for _, c := range p.All {
		if c.HasTemplate() {
			return true
		}
	}
	for _, c := range p.Any {
		if c.HasTemplate() {
			return true
		}
	}
	if p.Not != nil && p.Not.HasTemplate() {
		return true
	}
	for _, v := range p.Values {
		if v.Template != nil {
			return true
		}
	}
	return false
}

// compilePredicate lowers an apitypes.Predicate into a CompiledPredicate.
// Template values are lifted into Template objects; literals stay as-is.
func compilePredicate(p *apitypes.Predicate) (*CompiledPredicate, error) {
	if p == nil {
		return nil, nil
	}
	// Internal node.
	if len(p.All) > 0 || len(p.Any) > 0 || p.Not != nil {
		out := &CompiledPredicate{}
		for i := range p.All {
			c, err := compilePredicate(&p.All[i])
			if err != nil {
				return nil, err
			}
			if c != nil {
				out.All = append(out.All, c)
			}
		}
		for i := range p.Any {
			c, err := compilePredicate(&p.Any[i])
			if err != nil {
				return nil, err
			}
			if c != nil {
				out.Any = append(out.Any, c)
			}
		}
		if p.Not != nil {
			c, err := compilePredicate(p.Not)
			if err != nil {
				return nil, err
			}
			out.Not = c
		}
		return out, nil
	}

	// Leaf.
	if p.Column == "" {
		return nil, fmt.Errorf("predicate leaf: column is required")
	}
	if p.Op == "" {
		return nil, fmt.Errorf("predicate leaf %q: op is required", p.Column)
	}
	leaf := &CompiledPredicate{Column: p.Column, Op: p.Op}

	// Operators that take no value.
	switch p.Op {
	case apitypes.PredOpIsNull, apitypes.PredOpIsNotNull:
		return leaf, nil
	}

	// Operators that take exactly one value.
	switch p.Op {
	case apitypes.PredOpEquals, apitypes.PredOpNotEquals,
		apitypes.PredOpGreaterThan, apitypes.PredOpGreaterThanOrEqual,
		apitypes.PredOpLessThan, apitypes.PredOpLessThanOrEqual,
		apitypes.PredOpLike, apitypes.PredOpNotLike,
		apitypes.PredOpMatches, apitypes.PredOpStartsWith,
		apitypes.PredOpEndsWith:
		if p.Value == nil {
			return nil, fmt.Errorf("predicate %q %s: value is required", p.Column, p.Op)
		}
		vs, err := makeValueSource(p.Value)
		if err != nil {
			return nil, fmt.Errorf("predicate %q %s: %w", p.Column, p.Op, err)
		}
		leaf.Values = []ValueSource{vs}
		return leaf, nil
	}

	// Operators that take a list of values.
	switch p.Op {
	case apitypes.PredOpIn, apitypes.PredOpNotIn:
		if len(p.Values) == 0 {
			return nil, fmt.Errorf("predicate %q %s: values is required", p.Column, p.Op)
		}
		leaf.Values = make([]ValueSource, 0, len(p.Values))
		for _, v := range p.Values {
			vs, err := makeValueSource(v)
			if err != nil {
				return nil, fmt.Errorf("predicate %q %s: %w", p.Column, p.Op, err)
			}
			leaf.Values = append(leaf.Values, vs)
		}
		return leaf, nil
	case apitypes.PredOpBetween:
		if len(p.Values) != 2 {
			return nil, fmt.Errorf("predicate %q Between: exactly 2 values required, got %d", p.Column, len(p.Values))
		}
		leaf.Values = make([]ValueSource, 0, 2)
		for _, v := range p.Values {
			vs, err := makeValueSource(v)
			if err != nil {
				return nil, fmt.Errorf("predicate %q Between: %w", p.Column, err)
			}
			leaf.Values = append(leaf.Values, vs)
		}
		return leaf, nil
	}

	return nil, fmt.Errorf("predicate %q: unknown op %q", p.Column, p.Op)
}

// makeValueSource inspects v: strings that look like templates become
// Template; everything else stays a literal.
func makeValueSource(v any) (ValueSource, error) {
	if s, ok := v.(string); ok && looksLikeTemplate(s) {
		t, err := CompileTemplate(s)
		if err != nil {
			return ValueSource{}, err
		}
		return ValueSource{Template: t}, nil
	}
	return ValueSource{Literal: v}, nil
}
