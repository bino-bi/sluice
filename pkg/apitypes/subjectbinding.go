// SPDX-License-Identifier: Apache-2.0

package apitypes

// SubjectBinding configures how inbound credentials (JWT, API key) resolve
// into an authenticated subject.
type SubjectBinding struct {
	TypeMeta `yaml:",inline" json:",inline"`
	Metadata ObjectMeta         `yaml:"metadata" json:"metadata"`
	Spec     SubjectBindingSpec `yaml:"spec" json:"spec"`
}

// SubjectBindingSpec is the body of a SubjectBinding.
type SubjectBindingSpec struct {
	Issuer        string          `yaml:"issuer,omitempty" json:"issuer,omitempty"`
	Audience      string          `yaml:"audience,omitempty" json:"audience,omitempty"`
	JWKSURL       string          `yaml:"jwksUrl,omitempty" json:"jwksUrl,omitempty"`
	HMACSecretRef string          `yaml:"hmacSecretRef,omitempty" json:"hmacSecretRef,omitempty"`
	JWKSCacheTTL  Duration        `yaml:"jwksCacheTtl,omitempty" json:"jwksCacheTtl,omitempty"`
	ClockSkew     Duration        `yaml:"clockSkew,omitempty" json:"clockSkew,omitempty"`
	Claims        ClaimPaths      `yaml:"claims,omitempty" json:"claims,omitempty"`
	GroupsSource  string          `yaml:"groupsSource,omitempty" json:"groupsSource,omitempty"`
	RateLimit     *RateLimitSpec  `yaml:"rateLimit,omitempty" json:"rateLimit,omitempty"`
	Budget        *BudgetSpec     `yaml:"budget,omitempty" json:"budget,omitempty"`
	APIKeys       []APIKeyBinding `yaml:"apiKeys,omitempty" json:"apiKeys,omitempty"`
}

// ClaimPaths maps well-known subject attributes onto JWT claim paths.
// Additional application-specific mappings spill into Extra.
type ClaimPaths struct {
	SubjectID      string            `yaml:"subjectId,omitempty" json:"subjectId,omitempty"`
	Email          string            `yaml:"email,omitempty" json:"email,omitempty"`
	Groups         string            `yaml:"groups,omitempty" json:"groups,omitempty"`
	TenantID       string            `yaml:"tenantId,omitempty" json:"tenantId,omitempty"`
	AllowedRegions string            `yaml:"allowedRegions,omitempty" json:"allowedRegions,omitempty"`
	Extra          map[string]string `yaml:",inline" json:"-"`
}

// RateLimitSpec caps request frequency for a subject.
type RateLimitSpec struct {
	RPS   float64 `yaml:"rps" json:"rps"`
	Burst int     `yaml:"burst" json:"burst"`
}

// BudgetSpec caps resource consumption per day. v2 wiring.
type BudgetSpec struct {
	CPUSecondsPerDay int64 `yaml:"cpuSecondsPerDay,omitempty" json:"cpuSecondsPerDay,omitempty"`
	RowsPerDay       int64 `yaml:"rowsPerDay,omitempty" json:"rowsPerDay,omitempty"`
}

// APIKeyBinding maps an API key to an identity. HashRef points at the
// bcrypt/scrypt hash of the key material stored via secret:// URI.
type APIKeyBinding struct {
	ID      string   `yaml:"id" json:"id"`
	HashRef string   `yaml:"hashRef" json:"hashRef"`
	Groups  []string `yaml:"groups,omitempty" json:"groups,omitempty"`
}

// GetTypeMeta implements Object.
func (s *SubjectBinding) GetTypeMeta() TypeMeta { return s.TypeMeta }

// GetObjectMeta implements Object.
func (s *SubjectBinding) GetObjectMeta() ObjectMeta { return s.Metadata }

// GetKind implements Object.
func (s *SubjectBinding) GetKind() Kind { return KindSubjectBinding }
