// SPDX-License-Identifier: Apache-2.0

package apitypes

// QueryRejectPolicy rejects entire queries that match one of its rules.
// Where ColumnMask/RowFilter shape the result, Reject kills the request
// before it runs.
type QueryRejectPolicy struct {
	TypeMeta `yaml:",inline" json:",inline"`
	Metadata ObjectMeta      `yaml:"metadata" json:"metadata"`
	Spec     QueryRejectSpec `yaml:"spec" json:"spec"`
}

// QueryRejectSpec is the body of a QueryRejectPolicy.
type QueryRejectSpec struct {
	EnforcementMode EnforcementMode `yaml:"enforcementMode,omitempty" json:"enforcementMode,omitempty"`
	Match           Selector        `yaml:"match" json:"match"`
	Exclude         *Selector       `yaml:"exclude,omitempty" json:"exclude,omitempty"`
	Conditions      []Condition     `yaml:"conditions,omitempty" json:"conditions,omitempty"`
	Reject          RejectSpec      `yaml:"reject" json:"reject"`
}

// RejectSpec carries the named rules to evaluate.
type RejectSpec struct {
	Rules []RejectRule `yaml:"rules" json:"rules"`
}

// RejectRule is a single, named reject predicate. Expression is evaluated
// against the parsed SQL AST by internal/policy; the exact language lands
// with v1.
type RejectRule struct {
	Name       string `yaml:"name" json:"name"`
	Expression string `yaml:"expression" json:"expression"`
	Message    string `yaml:"message,omitempty" json:"message,omitempty"`
	Code       string `yaml:"code,omitempty" json:"code,omitempty"`
}

// GetTypeMeta implements Object.
func (p *QueryRejectPolicy) GetTypeMeta() TypeMeta { return p.TypeMeta }

// GetObjectMeta implements Object.
func (p *QueryRejectPolicy) GetObjectMeta() ObjectMeta { return p.Metadata }

// GetKind implements Object.
func (p *QueryRejectPolicy) GetKind() Kind { return KindQueryRejectPolicy }
