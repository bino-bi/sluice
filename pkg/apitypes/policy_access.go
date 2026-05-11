// SPDX-License-Identifier: Apache-2.0

package apitypes

// SQLAccessPolicy grants or denies the right to touch a resource at all.
// If no SQLAccessPolicy matches, the request is denied (default-deny).
type SQLAccessPolicy struct {
	TypeMeta `yaml:",inline" json:",inline"`
	Metadata ObjectMeta    `yaml:"metadata" json:"metadata"`
	Spec     SQLAccessSpec `yaml:"spec" json:"spec"`
}

// SQLAccessSpec is the body of an SQLAccessPolicy.
type SQLAccessSpec struct {
	EnforcementMode EnforcementMode `yaml:"enforcementMode,omitempty" json:"enforcementMode,omitempty"`
	Effect          Effect          `yaml:"effect" json:"effect"`
	Match           Selector        `yaml:"match" json:"match"`
	Exclude         *Selector       `yaml:"exclude,omitempty" json:"exclude,omitempty"`
	Conditions      []Condition     `yaml:"conditions,omitempty" json:"conditions,omitempty"`
	Message         string          `yaml:"message,omitempty" json:"message,omitempty"`
	ErrorCode       string          `yaml:"errorCode,omitempty" json:"errorCode,omitempty"`
}

// GetTypeMeta implements Object.
func (p *SQLAccessPolicy) GetTypeMeta() TypeMeta { return p.TypeMeta }

// GetObjectMeta implements Object.
func (p *SQLAccessPolicy) GetObjectMeta() ObjectMeta { return p.Metadata }

// GetKind implements Object.
func (p *SQLAccessPolicy) GetKind() Kind { return KindSQLAccessPolicy }
