// SPDX-License-Identifier: Apache-2.0

package mask

import "fmt"

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

// ValidateArgs accepts algorithm "" (defaults to sha256) or "sha256".
// "hmac_sha256" is rejected until the post-query mask path lands.
func (hashProvider) ValidateArgs(a Args) error {
	switch a.Algorithm {
	case "", "sha256":
		return nil
	case "hmac_sha256":
		return fmt.Errorf("%w: algorithm hmac_sha256 requires the post-query mask path (not yet enabled)", ErrInvalidArgs)
	default:
		return fmt.Errorf("%w: algorithm %q unknown (use sha256)", ErrInvalidArgs, a.Algorithm)
	}
}
