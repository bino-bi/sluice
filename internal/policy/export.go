// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgmask "github.com/bino-bi/sluice/pkg/mask"
)

// These thin exports let alternative engines (internal/opaengine,
// internal/rebac) reuse the YAML engine's proven compile paths so their
// decisions flow through the identical parameterised-SQL and mask
// machinery — no engine renders SQL text itself.

// CompilePredicateSpec compiles an apitypes.Predicate into the engine's
// CompiledPredicate tree (subject/request templates, positional params).
func CompilePredicateSpec(p *apitypes.Predicate) (*CompiledPredicate, error) {
	return compilePredicate(p)
}

// CompileMaskSpec validates an apitypes.MaskSpec and returns the mirrored
// mask.Args. Only providers enabled for the SQL path are accepted.
func CompileMaskSpec(spec apitypes.MaskSpec) (pkgmask.Args, error) {
	return compileMaskArgs(spec)
}

// CompileSelectorSpec compiles an apitypes.Selector into a CompiledSelector.
func CompileSelectorSpec(sel apitypes.Selector) (CompiledSelector, error) {
	return compileSelector(sel)
}

// NewFilter builds a CompiledFilter from a compiled predicate. Alternative
// engines use this to contribute row filters that merge exactly like YAML
// ones.
func NewFilter(tableKey string, pred *CompiledPredicate, combine apitypes.Combine, policy string) *CompiledFilter {
	return &CompiledFilter{TableKey: tableKey, Predicate: pred, Combine: combine, Policies: []string{policy}}
}

// NewMask builds a CompiledMask for a column an alternative engine masks.
func NewMask(tableKey, column string, maskType apitypes.MaskType, args pkgmask.Args, policy string) *CompiledMask {
	return &CompiledMask{TableKey: tableKey, Column: column, Type: maskType, Args: args, Policy: policy}
}
