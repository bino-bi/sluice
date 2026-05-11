// SPDX-License-Identifier: Apache-2.0

package mask

// Args is the validated argument payload passed to a mask Provider. It
// mirrors apitypes.MaskArgs field-for-field (plus the provider-synthesized
// Expression and Extras). The mirror invariant is enforced by the test in
// mirror_test.go: if a field is added to apitypes.MaskArgs and not here
// (or vice versa), CI fails.
//
// Keeping this type in pkg/mask (rather than importing apitypes) is
// deliberate: pkg/mask stays usable by third parties that don't want to
// depend on the policy DSL types.
type Args struct {
	// --- constant ---
	Value any

	// --- partial ---
	ShowFirst int
	ShowLast  int
	MaskChar  string

	// --- hash ---
	Algorithm string // "sha256" | "hmac_sha256"
	SaltRef   string

	// --- regex (v1) ---
	Pattern     string
	Replacement string

	// --- truncate (v1) ---
	Length int
	Suffix string

	// --- jitter (v1) ---
	Range float64
	Seed  string

	// --- fpe (v1) ---
	KeyRef         string
	Tweak          string
	Alphabet       string
	CustomAlphabet string

	// --- fake (v1) ---
	FakeType string

	// --- external (v2) ---
	Provider string
	KeyPath  string

	// Expression is synthesized by the rewriter for the "custom" provider
	// (apitypes.MaskSpec.Expression lives alongside Args on the policy
	// side; the provider receives both through this single type).
	Expression string

	// Extras preserves forward-compat args. Populated from
	// apitypes.MaskArgs.Extras by the rewriter.
	Extras map[string]any
}
