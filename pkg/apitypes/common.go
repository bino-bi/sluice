// SPDX-License-Identifier: Apache-2.0

package apitypes

// Selector expresses a match or exclude set. Exactly one of Any or All is
// usually populated; an object where neither is populated matches nothing.
type Selector struct {
	Any []Clause `yaml:"any,omitempty" json:"any,omitempty"`
	All []Clause `yaml:"all,omitempty" json:"all,omitempty"`
}

// Clause binds a subject-side and/or resource-side selector.
type Clause struct {
	Subjects  *SubjectSelector  `yaml:"subjects,omitempty" json:"subjects,omitempty"`
	Resources *ResourceSelector `yaml:"resources,omitempty" json:"resources,omitempty"`
}

// SubjectSelector matches on authenticated subject attributes.
type SubjectSelector struct {
	JWTClaims []ClaimCheck `yaml:"jwtClaims,omitempty" json:"jwtClaims,omitempty"`
	Groups    []string     `yaml:"groups,omitempty" json:"groups,omitempty"`
	APIKeys   []string     `yaml:"apiKeys,omitempty" json:"apiKeys,omitempty"`
	IPRanges  []string     `yaml:"ipRanges,omitempty" json:"ipRanges,omitempty"`
	Roles     []string     `yaml:"roles,omitempty" json:"roles,omitempty"`
}

// ClaimCheck evaluates a single JWT claim with the given operator.
type ClaimCheck struct {
	Claim   string  `yaml:"claim" json:"claim"`
	Op      ClaimOp `yaml:"op" json:"op"`
	Value   any     `yaml:"value,omitempty" json:"value,omitempty"`
	Values  []any   `yaml:"values,omitempty" json:"values,omitempty"`
	Pattern string  `yaml:"pattern,omitempty" json:"pattern,omitempty"`
}

// ClaimOp names a JWT claim operator.
type ClaimOp string

const (
	ClaimOpEquals    ClaimOp = "Equals"
	ClaimOpNotEquals ClaimOp = "NotEquals"
	ClaimOpIn        ClaimOp = "In"
	ClaimOpNotIn     ClaimOp = "NotIn"
	ClaimOpExists    ClaimOp = "Exists"
	ClaimOpMatches   ClaimOp = "Matches"
)

// ResourceSelector matches on resource identifiers and requested actions.
// Catalog/Schema/Table/Column values accept wildcard patterns (see
// CompileWildcard).
type ResourceSelector struct {
	Catalogs []string `yaml:"catalogs,omitempty" json:"catalogs,omitempty"`
	Schemas  []string `yaml:"schemas,omitempty" json:"schemas,omitempty"`
	Tables   []string `yaml:"tables,omitempty" json:"tables,omitempty"`
	Columns  []string `yaml:"columns,omitempty" json:"columns,omitempty"`
	Tags     []string `yaml:"tags,omitempty" json:"tags,omitempty"`
	Actions  []Action `yaml:"actions,omitempty" json:"actions,omitempty"`
}

// Action names an SQL operation.
type Action string

const (
	ActionSelect Action = "SELECT"
	ActionInsert Action = "INSERT"
	ActionUpdate Action = "UPDATE"
	ActionDelete Action = "DELETE"
)

// Condition is an arbitrary named expression evaluated in policy context.
// The Expression string is not parsed here; internal/policy interprets it
// (CEL or plain boolean SQL, depending on version).
type Condition struct {
	Name       string `yaml:"name" json:"name"`
	Expression string `yaml:"expression" json:"expression"`
}

// EnforcementMode controls whether a policy actually denies or merely audits.
// EnforcementAudit and EnforcementDryRun land in v1; the constants are
// declared now so YAML files targeting v1 parse cleanly.
type EnforcementMode string

const (
	EnforcementEnforce EnforcementMode = "Enforce"
	EnforcementAudit   EnforcementMode = "Audit"
	EnforcementDryRun  EnforcementMode = "DryRun"
)

// Effect is the outcome of an SqlAccessPolicy match.
type Effect string

const (
	EffectAllow Effect = "allow"
	EffectDeny  Effect = "deny"
)

// Combine controls how multiple row filters are composed on the same table.
type Combine string

const (
	// CombineRestrictive ANDs all filters (default).
	CombineRestrictive Combine = "restrictive"
	// CombinePermissive ORs all filters.
	CombinePermissive Combine = "permissive"
)
