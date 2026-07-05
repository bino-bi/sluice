// SPDX-License-Identifier: Apache-2.0

package mask

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
)

// jitterProvider adds deterministic keyed noise to a numeric value so the
// same input always jitters to the same output (joins and GROUP BY stay
// consistent) while the true value is obscured. Post-query only.
type jitterProvider struct{}

func newJitterProvider() Provider { return &jitterProvider{} }

// Type returns "jitter".
func (jitterProvider) Type() string { return "jitter" }

// MaskSQL always reports post-query-only.
func (jitterProvider) MaskSQL(_ MaskContext) (string, []Param, error) {
	return "", nil, ErrPostQueryOnly
}

// MaskArrow is not supported; the post-query path is NewRowMask.
func (jitterProvider) MaskArrow(_ MaskArrowContext) error { return ErrSQLOnly }

// ValidateArgs requires a positive range in (0, 1).
func (jitterProvider) ValidateArgs(a Args) error {
	if a.Range <= 0 || a.Range >= 1 {
		return fmt.Errorf("%w: range must be in (0, 1), got %v", ErrInvalidArgs, a.Range)
	}
	return nil
}

// NewRowMask resolves the seed and returns a masker.
func (jitterProvider) NewRowMask(ctx RowMaskContext) (RowMask, error) {
	seed := []byte(ctx.Args.Seed)
	if ctx.Args.Seed != "" && ctx.Salts != nil && looksLikeSecretRef(ctx.Args.Seed) {
		b, err := ctx.Salts.Get(ctx.Ctx, ctx.Args.Seed)
		if err != nil {
			return nil, fmt.Errorf("resolve jitter seed: %w", err)
		}
		seed = b
	}
	return &jitterRowMask{seed: seed, rng: ctx.Args.Range}, nil
}

func looksLikeSecretRef(s string) bool {
	return len(s) > len("secret://") && s[:len("secret://")] == "secret://"
}

type jitterRowMask struct {
	seed []byte
	rng  float64
}

// Mask scales the value by (1 + f) where f is a deterministic uniform in
// [-rng, +rng] derived from HMAC(seed, canonical-value). Integer inputs
// stay integers (rounded); non-numeric values fail closed.
func (m *jitterRowMask) Mask(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	f := m.factor(value)
	switch v := value.(type) {
	case int:
		return int(math.Round(float64(v) * (1 + f))), nil
	case int32:
		return int32(math.Round(float64(v) * (1 + f))), nil
	case int64:
		return int64(math.Round(float64(v) * (1 + f))), nil
	case float32:
		return float32(float64(v) * (1 + f)), nil
	case float64:
		return v * (1 + f), nil
	default:
		return nil, fmt.Errorf("%w: jitter requires a numeric column, got %T", ErrInvalidArgs, value)
	}
}

func (m *jitterRowMask) factor(value any) float64 {
	h := hmac.New(sha256.New, m.seed)
	_, _ = fmt.Fprintf(h, "%v", value)
	sum := h.Sum(nil)
	u := binary.BigEndian.Uint64(sum[:8])
	// Map to [0, 1) then to [-rng, +rng].
	unit := float64(u) / float64(math.MaxUint64)
	return (unit*2 - 1) * m.rng
}
