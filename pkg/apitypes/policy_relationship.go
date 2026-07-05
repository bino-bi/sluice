// SPDX-License-Identifier: Apache-2.0

package apitypes

// RelationshipPolicy gates access via a ReBAC backend (OpenFGA / SpiceDB):
// for each matched table, Sluice asks the backend whether the subject has
// the required relation to the table object. It is a v1beta1 kind resolved
// by internal/rebac as a composite policy-engine member.
type RelationshipPolicy struct {
	TypeMeta `yaml:",inline" json:",inline"`
	Metadata ObjectMeta       `yaml:"metadata" json:"metadata"`
	Spec     RelationshipSpec `yaml:"spec" json:"spec"`
}

// RelationshipSpec is the body of a RelationshipPolicy.
type RelationshipSpec struct {
	EnforcementMode EnforcementMode     `yaml:"enforcementMode,omitempty" json:"enforcementMode,omitempty"`
	Match           Selector            `yaml:"match" json:"match"`
	Exclude         *Selector           `yaml:"exclude,omitempty" json:"exclude,omitempty"`
	Backend         RelationshipBackend `yaml:"backend" json:"backend"`
	Checks          []RelationCheck     `yaml:"checks" json:"checks"`
}

// RelationshipBackend addresses the ReBAC service.
type RelationshipBackend struct {
	Type                 string   `yaml:"type" json:"type"` // "openfga"
	Endpoint             string   `yaml:"endpoint" json:"endpoint"`
	StoreID              string   `yaml:"storeId" json:"storeId"`
	AuthorizationModelID string   `yaml:"authorizationModelId,omitempty" json:"authorizationModelId,omitempty"`
	TokenRef             string   `yaml:"tokenRef,omitempty" json:"tokenRef,omitempty"`
	Timeout              Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	CacheTTL             Duration `yaml:"cacheTtl,omitempty" json:"cacheTtl,omitempty"`
}

// RelationCheck is one relationship assertion. ObjectTemplate and
// SubjectTemplate accept the placeholders {{catalog}}, {{schema}},
// {{table}}, {{subject.id}}, {{subject.email}}. SubjectTemplate defaults
// to "user:{{subject.id}}".
type RelationCheck struct {
	ObjectTemplate  string `yaml:"objectTemplate" json:"objectTemplate"`
	Relation        string `yaml:"relation" json:"relation"`
	SubjectTemplate string `yaml:"subjectTemplate,omitempty" json:"subjectTemplate,omitempty"`
}

// GetTypeMeta implements Object.
func (p *RelationshipPolicy) GetTypeMeta() TypeMeta { return p.TypeMeta }

// GetObjectMeta implements Object.
func (p *RelationshipPolicy) GetObjectMeta() ObjectMeta { return p.Metadata }

// GetKind implements Object.
func (p *RelationshipPolicy) GetKind() Kind { return KindRelationshipPolicy }
