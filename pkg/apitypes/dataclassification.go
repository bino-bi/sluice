// SPDX-License-Identifier: Apache-2.0

package apitypes

// DataClassification assigns tags to resources so policies can match on
// tags (ResourceSelector.Tags) instead of concrete names. Tags are
// resolved at policy-compile time — a v1beta1 kind consumed by the policy
// compiler, not evaluated at runtime.
type DataClassification struct {
	TypeMeta `yaml:",inline" json:",inline"`
	Metadata ObjectMeta             `yaml:"metadata" json:"metadata"`
	Spec     DataClassificationSpec `yaml:"spec" json:"spec"`
}

// DataClassificationSpec is the body of a DataClassification.
type DataClassificationSpec struct {
	Rules []ClassificationRule `yaml:"rules" json:"rules"`
}

// ClassificationRule tags the resources its selector matches. The
// selector's Tags and Actions fields must be empty (no recursion, no
// action scoping inside a classification).
type ClassificationRule struct {
	Resources ResourceSelector `yaml:"resources" json:"resources"`
	Tags      []string         `yaml:"tags" json:"tags"`
}

// GetTypeMeta implements Object.
func (p *DataClassification) GetTypeMeta() TypeMeta { return p.TypeMeta }

// GetObjectMeta implements Object.
func (p *DataClassification) GetObjectMeta() ObjectMeta { return p.Metadata }

// GetKind implements Object.
func (p *DataClassification) GetKind() Kind { return KindDataClassification }
