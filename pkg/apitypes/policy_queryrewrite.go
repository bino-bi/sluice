// SPDX-License-Identifier: Apache-2.0

package apitypes

// QueryRewritePolicy attaches LIMIT, sampling, timeouts, or hints to queries
// that match the selector. Runtime support lands in v1; the type is
// declared here so MVP configs may reference future policies without
// breaking once the engine catches up.
type QueryRewritePolicy struct {
	TypeMeta `yaml:",inline" json:",inline"`
	Metadata ObjectMeta       `yaml:"metadata" json:"metadata"`
	Spec     QueryRewriteSpec `yaml:"spec" json:"spec"`
}

// QueryRewriteSpec is the body of a QueryRewritePolicy.
type QueryRewriteSpec struct {
	EnforcementMode EnforcementMode `yaml:"enforcementMode,omitempty" json:"enforcementMode,omitempty"`
	Match           Selector        `yaml:"match" json:"match"`
	Exclude         *Selector       `yaml:"exclude,omitempty" json:"exclude,omitempty"`
	Conditions      []Condition     `yaml:"conditions,omitempty" json:"conditions,omitempty"`
	Rewrite         RewriteSpec     `yaml:"rewrite" json:"rewrite"`
}

// RewriteSpec carries the rewrite operations to apply.
type RewriteSpec struct {
	Limit   *LimitSpec  `yaml:"limit,omitempty" json:"limit,omitempty"`
	Sample  *SampleSpec `yaml:"sample,omitempty" json:"sample,omitempty"`
	Timeout Duration    `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Hints   []HintEntry `yaml:"hint,omitempty" json:"hint,omitempty"`
}

// LimitSpec caps the number of rows returned.
type LimitSpec struct {
	Max int64 `yaml:"max" json:"max"`
}

// SampleSpec rewrites the query to return a sample of rows.
type SampleSpec struct {
	Rate   float64    `yaml:"rate" json:"rate"`
	Method SampleMode `yaml:"method,omitempty" json:"method,omitempty"`
}

// SampleMode names a sampling method.
type SampleMode string

const (
	SampleReservoir SampleMode = "reservoir"
	SampleBernoulli SampleMode = "bernoulli"
	SampleSystem    SampleMode = "system"
)

// HintEntry is a generic (key, value) rewrite hint.
type HintEntry struct {
	Key   string `yaml:"key" json:"key"`
	Value string `yaml:"value" json:"value"`
}

// GetTypeMeta implements Object.
func (p *QueryRewritePolicy) GetTypeMeta() TypeMeta { return p.TypeMeta }

// GetObjectMeta implements Object.
func (p *QueryRewritePolicy) GetObjectMeta() ObjectMeta { return p.Metadata }

// GetKind implements Object.
func (p *QueryRewritePolicy) GetKind() Kind { return KindQueryRewritePolicy }
