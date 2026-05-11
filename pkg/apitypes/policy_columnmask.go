// SPDX-License-Identifier: Apache-2.0

package apitypes

// ColumnMaskPolicy replaces the values of matched columns with a masked
// representation before returning them to the client.
type ColumnMaskPolicy struct {
	TypeMeta `yaml:",inline" json:",inline"`
	Metadata ObjectMeta     `yaml:"metadata" json:"metadata"`
	Spec     ColumnMaskSpec `yaml:"spec" json:"spec"`
}

// ColumnMaskSpec is the body of a ColumnMaskPolicy.
type ColumnMaskSpec struct {
	EnforcementMode EnforcementMode `yaml:"enforcementMode,omitempty" json:"enforcementMode,omitempty"`
	Match           Selector        `yaml:"match" json:"match"`
	Exclude         *Selector       `yaml:"exclude,omitempty" json:"exclude,omitempty"`
	Conditions      []Condition     `yaml:"conditions,omitempty" json:"conditions,omitempty"`
	Mask            MaskSpec        `yaml:"mask" json:"mask"`
}

// MaskSpec selects a mask type and carries its parameters.
type MaskSpec struct {
	Type       MaskType `yaml:"type" json:"type"`
	Args       MaskArgs `yaml:"args,omitempty" json:"args,omitempty"`
	Expression string   `yaml:"expression,omitempty" json:"expression,omitempty"`
}

// MaskType identifies a mask provider. The `mvp` / `v1` / `v2` comments
// mark when each is wired up in the runtime; the YAML DSL accepts all of
// them from day one so policy files do not need a version bump.
type MaskType string

const (
	MaskNull     MaskType = "null"     // MVP
	MaskConstant MaskType = "constant" // MVP
	MaskPartial  MaskType = "partial"  // MVP
	MaskHash     MaskType = "hash"     // MVP
	MaskRegex    MaskType = "regex"    // v1
	MaskTruncate MaskType = "truncate" // v1
	MaskJitter   MaskType = "jitter"   // v1
	MaskFPE      MaskType = "fpe"      // v1
	MaskFake     MaskType = "fake"     // v1
	MaskCustom   MaskType = "custom"   // v1
	MaskExternal MaskType = "external" // v2
)

// MaskArgs is a discriminated union over all mask-type parameters. On
// decode, only the fields relevant to MaskSpec.Type are populated; the rest
// stay at their zero values. Unknown fields spill into Extras so policy
// files written for a future sluice version still parse.
//
// This struct is mirrored field-for-field by mask.Args in pkg/mask. A
// reflect-based test in pkg/mask fails CI if the two drift.
type MaskArgs struct {
	// constant
	Value any `yaml:"value,omitempty" json:"value,omitempty"`

	// partial
	ShowFirst int    `yaml:"showFirst,omitempty" json:"showFirst,omitempty"`
	ShowLast  int    `yaml:"showLast,omitempty" json:"showLast,omitempty"`
	MaskChar  string `yaml:"maskChar,omitempty" json:"maskChar,omitempty"`

	// hash
	Algorithm HashAlgo `yaml:"algorithm,omitempty" json:"algorithm,omitempty"`
	SaltRef   string   `yaml:"saltRef,omitempty" json:"saltRef,omitempty"`

	// regex (v1)
	Pattern     string `yaml:"pattern,omitempty" json:"pattern,omitempty"`
	Replacement string `yaml:"replacement,omitempty" json:"replacement,omitempty"`

	// truncate (v1)
	Length int    `yaml:"length,omitempty" json:"length,omitempty"`
	Suffix string `yaml:"suffix,omitempty" json:"suffix,omitempty"`

	// jitter (v1)
	Range float64 `yaml:"range,omitempty" json:"range,omitempty"`
	Seed  string  `yaml:"seed,omitempty" json:"seed,omitempty"`

	// fpe (v1)
	KeyRef         string `yaml:"keyRef,omitempty" json:"keyRef,omitempty"`
	Tweak          string `yaml:"tweak,omitempty" json:"tweak,omitempty"`
	Alphabet       string `yaml:"alphabet,omitempty" json:"alphabet,omitempty"`
	CustomAlphabet string `yaml:"customAlphabet,omitempty" json:"customAlphabet,omitempty"`

	// fake (v1)
	FakeType string `yaml:"fakeType,omitempty" json:"fakeType,omitempty"`

	// external (v2)
	Provider string `yaml:"provider,omitempty" json:"provider,omitempty"`
	KeyPath  string `yaml:"keyPath,omitempty" json:"keyPath,omitempty"`

	// Extras preserves unknown fields so policy files written for future
	// versions of sluice still parse. sigs.k8s.io/yaml uses the JSON
	// inline convention; go-yaml uses `,inline` on a map.
	Extras map[string]any `yaml:",inline" json:"-"`
}

// HashAlgo names a hash algorithm for MaskHash.
type HashAlgo string

const (
	HashSHA256     HashAlgo = "sha256"
	HashHMACSHA256 HashAlgo = "hmac_sha256"
)

// maskArgsKnownKeys lists the JSON names of every declared MaskArgs field.
// Fields that are not in this set are preserved in Extras on decode so
// policy files written for a future sluice version still parse.
var maskArgsKnownKeys = map[string]struct{}{
	"value":          {},
	"showFirst":      {},
	"showLast":       {},
	"maskChar":       {},
	"algorithm":      {},
	"saltRef":        {},
	"pattern":        {},
	"replacement":    {},
	"length":         {},
	"suffix":         {},
	"range":          {},
	"seed":           {},
	"keyRef":         {},
	"tweak":          {},
	"alphabet":       {},
	"customAlphabet": {},
	"fakeType":       {},
	"provider":       {},
	"keyPath":        {},
}

// GetTypeMeta implements Object.
func (p *ColumnMaskPolicy) GetTypeMeta() TypeMeta { return p.TypeMeta }

// GetObjectMeta implements Object.
func (p *ColumnMaskPolicy) GetObjectMeta() ObjectMeta { return p.Metadata }

// GetKind implements Object.
func (p *ColumnMaskPolicy) GetKind() Kind { return KindColumnMaskPolicy }
