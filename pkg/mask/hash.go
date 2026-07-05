// SPDX-License-Identifier: Apache-2.0

package mask

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// hashProvider replaces a value with its hex digest. Algorithm "sha256"
// runs in SQL (optionally salted via SaltRef); "hmac_sha256" requires the
// post-query mask path, which is not enabled yet.
type hashProvider struct{}

func newHashProvider() Provider { return &hashProvider{} }

// Type returns "hash".
func (hashProvider) Type() string { return "hash" }

// MaskSQL emits sha256 over the column, prefixing the resolved salt as a
// bound parameter when SaltRef is set. The salt travels only as a bind
// param — it never appears in the SQL text, logs, or audit records.
func (hashProvider) MaskSQL(ctx MaskContext) (string, []Param, error) {
	if alg := ctx.Args.Algorithm; alg != "" && alg != "sha256" {
		return "", nil, fmt.Errorf("%w: algorithm %q has no SQL path", ErrInvalidArgs, alg)
	}
	if ctx.Args.SaltRef == "" {
		return "sha256(__col__::VARCHAR)", nil, nil
	}
	if ctx.SaltStore == nil {
		return "", nil, fmt.Errorf("%w: saltRef set but no salt store configured", ErrInvalidArgs)
	}
	salt, err := ctx.SaltStore.Get(ctx.Ctx, ctx.Args.SaltRef)
	if err != nil {
		return "", nil, fmt.Errorf("resolve saltRef: %w", err)
	}
	return "sha256(concat($1, __col__::VARCHAR))",
		[]Param{{Name: "salt", Value: string(salt)}}, nil
}

// MaskArrow is not supported by this provider.
func (hashProvider) MaskArrow(_ MaskArrowContext) error {
	return ErrSQLOnly
}

// ValidateArgs accepts algorithm "" (defaults to sha256), "sha256", or
// "hmac_sha256". The HMAC variant requires a saltRef (the HMAC key) and
// runs post-query.
func (hashProvider) ValidateArgs(a Args) error {
	switch a.Algorithm {
	case "", "sha256":
		return nil
	case "hmac_sha256":
		if a.SaltRef == "" {
			return fmt.Errorf("%w: hmac_sha256 requires saltRef (the HMAC key)", ErrInvalidArgs)
		}
		return nil
	default:
		return fmt.Errorf("%w: algorithm %q unknown (use sha256 or hmac_sha256)", ErrInvalidArgs, a.Algorithm)
	}
}

// PostQuery reports whether these args select the post-query path. Only
// hmac_sha256 does; plain sha256 runs in SQL.
func (hashProvider) PostQuery(a Args) bool {
	return a.Algorithm == "hmac_sha256"
}

// NewRowMask builds the HMAC-SHA256 masker, resolving the key from the
// salt store.
func (hashProvider) NewRowMask(ctx RowMaskContext) (RowMask, error) {
	if ctx.Salts == nil {
		return nil, fmt.Errorf("%w: hmac_sha256 requires a salt store", ErrInvalidArgs)
	}
	key, err := ctx.Salts.Get(ctx.Ctx, ctx.Args.SaltRef)
	if err != nil {
		return nil, fmt.Errorf("resolve hmac key: %w", err)
	}
	return &hmacRowMask{key: key}, nil
}

type hmacRowMask struct{ key []byte }

// Mask returns the hex HMAC-SHA256 of the value.
func (m *hmacRowMask) Mask(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	h := hmac.New(sha256.New, m.key)
	_, _ = fmt.Fprintf(h, "%v", value)
	return hex.EncodeToString(h.Sum(nil)), nil
}
