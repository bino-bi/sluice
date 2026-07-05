// SPDX-License-Identifier: Apache-2.0

package apitypes

// GroupVersion identifies a schema group+version for Sluice policy objects.
type GroupVersion string

const (
	// GroupVersionAlpha1 is the group-version used by all MVP kinds.
	GroupVersionAlpha1 GroupVersion = "sluice.bino.bi/v1alpha1"
	// GroupVersionBeta1 is reserved for v1/v2 kinds (DataClassification,
	// RelationshipPolicy). Declared now so downstream code can reference it
	// without a breaking change when those kinds land.
	GroupVersionBeta1 GroupVersion = "sluice.bino.bi/v1beta1"
)

// Kind names a concrete object type within a GroupVersion.
type Kind string

const (
	KindSQLAccessPolicy    Kind = "SqlAccessPolicy"
	KindRowFilterPolicy    Kind = "RowFilterPolicy"
	KindColumnMaskPolicy   Kind = "ColumnMaskPolicy"
	KindQueryRejectPolicy  Kind = "QueryRejectPolicy"
	KindQueryRewritePolicy Kind = "QueryRewritePolicy"
	KindApprovalPolicy     Kind = "ApprovalPolicy"
	KindDataSource         Kind = "DataSource"
	KindSubjectBinding     Kind = "SubjectBinding"
	KindAuditSink          Kind = "AuditSink"
	KindDataClassification Kind = "DataClassification"
	KindRelationshipPolicy Kind = "RelationshipPolicy"
)

// TypeMeta is embedded in every top-level object to identify its schema.
type TypeMeta struct {
	APIVersion GroupVersion `yaml:"apiVersion" json:"apiVersion"`
	Kind       Kind         `yaml:"kind" json:"kind"`
}

// ObjectMeta carries object identity and optional organization metadata.
type ObjectMeta struct {
	Name        string            `yaml:"name" json:"name"`
	Namespace   string            `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
	Priority    int32             `yaml:"priority,omitempty" json:"priority,omitempty"`
}

// Object is implemented by every top-level YAML document. Concrete types
// embed TypeMeta and ObjectMeta (as a field named `Metadata`) and expose
// them via these accessors so callers can work against the interface.
type Object interface {
	GetTypeMeta() TypeMeta
	GetObjectMeta() ObjectMeta
	GetKind() Kind
}
