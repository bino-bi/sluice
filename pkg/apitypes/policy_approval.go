// SPDX-License-Identifier: Apache-2.0

package apitypes

// ApprovalPolicy marks queries that require human approval before they
// run. When the query matches the selector and the (optional) trigger
// conditions, Sluice fires a webhook carrying accept/reject capability
// URLs and holds the query. Multiple matching ApprovalPolicies aggregate
// into a single approval request.
type ApprovalPolicy struct {
	TypeMeta `yaml:",inline" json:",inline"`
	Metadata ObjectMeta   `yaml:"metadata" json:"metadata"`
	Spec     ApprovalSpec `yaml:"spec" json:"spec"`
}

// ApprovalSpec is the body of an ApprovalPolicy.
type ApprovalSpec struct {
	// EnforcementMode Audit/DryRun record a "would-have-required-approval"
	// shadow without holding the query; Enforce (default) gates it.
	EnforcementMode EnforcementMode `yaml:"enforcementMode,omitempty" json:"enforcementMode,omitempty"`
	Match           Selector        `yaml:"match" json:"match"`
	Exclude         *Selector       `yaml:"exclude,omitempty" json:"exclude,omitempty"`
	Conditions      []Condition     `yaml:"conditions,omitempty" json:"conditions,omitempty"`
	// When narrows the trigger beyond the selector: approval fires only if
	// one of the listed columns is accessed or one of the predicates
	// matches. An empty When means the selector match alone triggers.
	When *ApprovalWhen `yaml:"when,omitempty" json:"when,omitempty"`
	// Reason is human-readable text included in the webhook payload and
	// the client error details.
	Reason string `yaml:"reason,omitempty" json:"reason,omitempty"`
}

// ApprovalWhen carries the dynamic trigger conditions. ColumnsAccessed and
// Predicates are OR'd: any hit triggers approval.
type ApprovalWhen struct {
	// ColumnsAccessed are wildcard patterns matched against every column
	// the query references (bare or dotted-qualified name).
	ColumnsAccessed []string `yaml:"columnsAccessed,omitempty" json:"columnsAccessed,omitempty"`
	// Predicates trigger when a (column, op, value) comparison in the
	// query matches.
	Predicates []PredicateTrigger `yaml:"predicates,omitempty" json:"predicates,omitempty"`
}

// PredicateTrigger matches a WHERE/HAVING/JOIN comparison. Column is a
// wildcard pattern (required). Op empty or "*" matches any operator; else
// one of =,!=,<,<=,>,>=,like,ilike,in,isnull. Value empty matches any
// literal; else it must string-equal the normalised literal.
type PredicateTrigger struct {
	Column string `yaml:"column" json:"column"`
	Op     string `yaml:"op,omitempty" json:"op,omitempty"`
	Value  string `yaml:"value,omitempty" json:"value,omitempty"`
}

// GetTypeMeta implements Object.
func (p *ApprovalPolicy) GetTypeMeta() TypeMeta { return p.TypeMeta }

// GetObjectMeta implements Object.
func (p *ApprovalPolicy) GetObjectMeta() ObjectMeta { return p.Metadata }

// GetKind implements Object.
func (p *ApprovalPolicy) GetKind() Kind { return KindApprovalPolicy }
