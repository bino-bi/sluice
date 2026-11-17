// SPDX-License-Identifier: Apache-2.0

package mask

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"reflect"
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

// ValidateArgs requires a positive range in (0, 1) and a seed. Without a
// seed the HMAC key is empty and anyone can recompute the noise.
func (jitterProvider) ValidateArgs(a Args) error {
	if a.Range <= 0 || a.Range >= 1 {
		return fmt.Errorf("%w: range must be in (0, 1), got %v", ErrInvalidArgs, a.Range)
	}
	if a.Seed == "" {
		return fmt.Errorf("%w: seed required", ErrInvalidArgs)
	}
	return nil
}

// NewRowMask resolves the seed and returns a masker.
func (jitterProvider) NewRowMask(ctx RowMaskContext) (RowMask, error) {
	seed, err := resolveSeed(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve jitter seed: %w", err)
	}
	return &jitterRowMask{seed: seed, rng: ctx.Args.Range}, nil
}

func looksLikeSecretRef(s string) bool {
	return len(s) > len("secret://") && s[:len("secret://")] == "secret://"
}

// resolveSeed returns the HMAC key of a jitter or fake mask: the plain
// seed, or the secret a secret:// seed points to.
func resolveSeed(ctx RowMaskContext) ([]byte, error) {
	if !looksLikeSecretRef(ctx.Args.Seed) {
		return []byte(ctx.Args.Seed), nil
	}
	if ctx.Salts == nil {
		return nil, fmt.Errorf("%w: no salt store for a secret:// seed", ErrResolveSecret)
	}
	return ctx.Salts.Get(ctx.Ctx, ctx.Args.Seed)
}

type jitterRowMask struct {
	seed []byte
	rng  float64
}

// Mask scales the value by (1 + f) where f is a deterministic uniform in
// [-rng, +rng] derived from HMAC(seed, canonical-value). Integer and
// decimal inputs keep their Go type and scale (rounded, clamped to the
// type range); non-numeric values fail closed.
func (m *jitterRowMask) Mask(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	k := 1 + m.factor(value)
	switch v := value.(type) {
	case int:
		return jitterInt(v, k, math.MinInt, math.MaxInt), nil
	case int8:
		return jitterInt(v, k, math.MinInt8, math.MaxInt8), nil
	case int16:
		return jitterInt(v, k, math.MinInt16, math.MaxInt16), nil
	case int32:
		return jitterInt(v, k, math.MinInt32, math.MaxInt32), nil
	case int64:
		return jitterInt(v, k, math.MinInt64, math.MaxInt64), nil
	case uint:
		return jitterInt(v, k, 0, math.MaxUint), nil
	case uint8:
		return jitterInt(v, k, 0, math.MaxUint8), nil
	case uint16:
		return jitterInt(v, k, 0, math.MaxUint16), nil
	case uint32:
		return jitterInt(v, k, 0, math.MaxUint32), nil
	case uint64:
		return jitterInt(v, k, 0, math.MaxUint64), nil
	case float32:
		return float32(max(-math.MaxFloat32, min(float64(v)*k, math.MaxFloat32))), nil
	case float64:
		return v * k, nil
	case *big.Int:
		if v == nil {
			return nil, nil
		}
		return jitterBig(v, k), nil
	}
	if out, ok := jitterDecimal(value, k); ok {
		return out, nil
	}
	return nil, fmt.Errorf("%w: jitter requires a numeric column, got %T", ErrInvalidArgs, value)
}

type integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// jitterInt rounds v*k and clamps it to [lo, hi], so a TINYINT near its
// limit does not wrap around.
func jitterInt[T integer](v T, k float64, lo, hi T) T {
	r := math.Round(float64(v) * k)
	switch {
	case r <= float64(lo):
		return lo
	case r >= float64(hi):
		return hi
	}
	return T(r)
}

// jitterBig rounds v*k half away from zero. HUGEINT values exceed float64
// precision, so the math runs in big.Float.
func jitterBig(v *big.Int, k float64) *big.Int {
	f := new(big.Float).SetPrec(256).SetInt(v)
	f.Mul(f, big.NewFloat(k))
	half := big.NewFloat(0.5)
	if f.Sign() < 0 {
		half.Neg(half)
	}
	f.Add(f, half)
	out, _ := f.Int(nil)
	// Clamp to HUGEINT, or to UHUGEINT for an input above the HUGEINT range.
	hi := maxInt128
	if v.Cmp(maxInt128) > 0 {
		hi = maxUint128
	}
	switch {
	case out.Cmp(hi) > 0:
		return new(big.Int).Set(hi)
	case out.Cmp(minInt128) < 0:
		return new(big.Int).Set(minInt128)
	}
	return out
}

var (
	maxInt128  = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1))
	minInt128  = new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 127))
	maxUint128 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
)

// jitterDecimal handles duckdb.Decimal (Width, Scale uint8, Value
// *big.Int) by its shape. Importing duckdb-go here would make pkg/mask
// depend on cgo. The result keeps the type, width and scale; the
// unscaled value is clamped to the digits the width allows.
func jitterDecimal(value any, k float64) (any, bool) {
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Struct || rv.Type().Name() != "Decimal" {
		return nil, false
	}
	width := rv.FieldByName("Width")
	scale := rv.FieldByName("Scale")
	unscaled := rv.FieldByName("Value")
	if !width.IsValid() || width.Kind() != reflect.Uint8 ||
		!scale.IsValid() || scale.Kind() != reflect.Uint8 ||
		!unscaled.IsValid() || unscaled.Type() != reflect.TypeFor[*big.Int]() {
		return nil, false
	}
	n, _ := unscaled.Interface().(*big.Int)
	if n == nil {
		return nil, false
	}
	j := jitterBig(n, k)
	limit := new(big.Int).Exp(big.NewInt(10), new(big.Int).SetUint64(width.Uint()), nil)
	limit.Sub(limit, big.NewInt(1))
	if j.CmpAbs(limit) > 0 {
		j.Set(limit)
		if n.Sign() < 0 {
			j.Neg(j)
		}
	}
	out := reflect.New(rv.Type()).Elem()
	out.Set(rv)
	out.FieldByName("Value").Set(reflect.ValueOf(j))
	return out.Interface(), true
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
