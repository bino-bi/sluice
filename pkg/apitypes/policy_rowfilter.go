// SPDX-License-Identifier: Apache-2.0

package apitypes

// RowFilterPolicy narrows the rows returned from a matched resource by
// injecting a WHERE predicate into the rewritten SQL.
type RowFilterPolicy struct {
	TypeMeta `yaml:",inline" json:",inline"`
	Metadata ObjectMeta    `yaml:"metadata" json:"metadata"`
	Spec     RowFilterSpec `yaml:"spec" json:"spec"`
}

// RowFilterSpec is the body of a RowFilterPolicy.
type RowFilterSpec struct {
	EnforcementMode EnforcementMode `yaml:"enforcementMode,omitempty" json:"enforcementMode,omitempty"`
	Match           Selector        `yaml:"match" json:"match"`
	Exclude         *Selector       `yaml:"exclude,omitempty" json:"exclude,omitempty"`
	Conditions      []Condition     `yaml:"conditions,omitempty" json:"conditions,omitempty"`
	Filter          FilterSpec      `yaml:"filter" json:"filter"`
	Combine         Combine         `yaml:"combine,omitempty" json:"combine,omitempty"`
}

// FilterSpec carries either a structured predicate tree (MVP) or a CEL
// expression (v1). Exactly one of the two must be set.
type FilterSpec struct {
	Predicate  *Predicate `yaml:"predicate,omitempty" json:"predicate,omitempty"`
	Expression string     `yaml:"expression,omitempty" json:"expression,omitempty"`
}

// Predicate is a recursive boolean tree. Leaf predicates populate Column,
// Op, and Value/Values; internal nodes populate All/Any/Not.
type Predicate struct {
	All []Predicate `yaml:"all,omitempty" json:"all,omitempty"`
	Any []Predicate `yaml:"any,omitempty" json:"any,omitempty"`
	Not *Predicate  `yaml:"not,omitempty" json:"not,omitempty"`

	Column string `yaml:"column,omitempty" json:"column,omitempty"`
	Op     PredOp `yaml:"op,omitempty" json:"op,omitempty"`
	Value  any    `yaml:"value,omitempty" json:"value,omitempty"`
	Values []any  `yaml:"values,omitempty" json:"values,omitempty"`
}

// PredOp names a leaf predicate operator.
type PredOp string

const (
	PredOpEquals             PredOp = "Equals"
	PredOpNotEquals          PredOp = "NotEquals"
	PredOpIn                 PredOp = "In"
	PredOpNotIn              PredOp = "NotIn"
	PredOpGreaterThan        PredOp = "GreaterThan"
	PredOpGreaterThanOrEqual PredOp = "GreaterThanOrEqual"
	PredOpLessThan           PredOp = "LessThan"
	PredOpLessThanOrEqual    PredOp = "LessThanOrEqual"
	PredOpBetween            PredOp = "Between"
	PredOpLike               PredOp = "Like"
	PredOpNotLike            PredOp = "NotLike"
	PredOpIsNull             PredOp = "IsNull"
	PredOpIsNotNull          PredOp = "IsNotNull"
	PredOpMatches            PredOp = "Matches"
	PredOpStartsWith         PredOp = "StartsWith"
	PredOpEndsWith           PredOp = "EndsWith"
)

// GetTypeMeta implements Object.
func (p *RowFilterPolicy) GetTypeMeta() TypeMeta { return p.TypeMeta }

// GetObjectMeta implements Object.
func (p *RowFilterPolicy) GetObjectMeta() ObjectMeta { return p.Metadata }

// GetKind implements Object.
func (p *RowFilterPolicy) GetKind() Kind { return KindRowFilterPolicy }
