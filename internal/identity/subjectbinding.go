// SPDX-License-Identifier: AGPL-3.0-or-later

package identity

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// SecretResolver resolves secret:// references. *secrets.Resolver
// satisfies it. ResolveString trims trailing white space.
type SecretResolver interface {
	Resolve(ctx context.Context, ref string) ([]byte, error)
	ResolveString(ctx context.Context, ref string) (string, error)
}

// minHMACSecretBytes is the SHA-256 output size, the shortest key HS256
// should get.
const minHMACSecretBytes = 32

// BindingIssuer is the issuer of the subjects a binding makes, for the
// rate limit and budget keys: spec.issuer, or the binding name for API
// keys without one (as APIKeyBindings sets it). "" means any issuer.
func BindingIssuer(sb *apitypes.SubjectBinding) string {
	if sb.Spec.Issuer != "" {
		return sb.Spec.Issuer
	}
	if len(sb.Spec.APIKeys) > 0 {
		return sb.Metadata.Name
	}
	return ""
}

// HMACSecrets resolves each SubjectBinding's hmacSecretRef into the
// issuer→secret map the JWT identifier uses to verify HS256/HS384 tokens.
// Without this a binding that advertises an HS* issuer could never
// authenticate, since RS/ES keys come from JWKS and HMAC has no network
// path. A trailing newline (common in file-mounted secrets) is trimmed.
// A secret shorter than minHMACSecretBytes is an error: an empty key lets
// anyone sign a valid token.
func HMACSecrets(ctx context.Context, r SecretResolver, bindings []*apitypes.SubjectBinding) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, sb := range bindings {
		if sb == nil || sb.Spec.HMACSecretRef == "" {
			continue
		}
		// The JWT identifier looks the secret up by issuer only.
		issuer := sb.Spec.Issuer
		if issuer == "" {
			return nil, fmt.Errorf("binding %q: hmacSecretRef needs an issuer", sb.Metadata.Name)
		}
		sec, err := r.Resolve(ctx, sb.Spec.HMACSecretRef)
		if err != nil {
			return nil, fmt.Errorf("resolve hmacSecretRef for issuer %q: %w", issuer, err)
		}
		key := []byte(strings.TrimRight(string(sec), "\r\n"))
		if len(key) < minHMACSecretBytes {
			return nil, fmt.Errorf("hmacSecretRef for issuer %q: the secret has %d bytes, at least %d are required",
				issuer, len(key), minHMACSecretBytes)
		}
		out[issuer] = key
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// APIKeyBindings walks every SubjectBinding, resolves each
// apiKeys[].hashRef through r, and produces the flat APIKeyBinding slice
// the APIKeyIdentifier indexes on. Per-key resolution failures log a
// warning and skip the key rather than aborting the whole reload: one
// rotated-out key should never blank the table.
func APIKeyBindings(ctx context.Context, r SecretResolver, bindings []*apitypes.SubjectBinding, log *slog.Logger) ([]APIKeyBinding, error) {
	var out []APIKeyBinding
	for _, sb := range bindings {
		if sb == nil || len(sb.Spec.APIKeys) == 0 {
			continue
		}
		issuer := sb.Spec.Issuer
		if issuer == "" {
			issuer = sb.Metadata.Name
		}
		claims := apiKeyLiteralClaims(sb.Spec.Claims)
		for _, k := range sb.Spec.APIKeys {
			if k.ID == "" || k.HashRef == "" {
				log.Warn("api-key binding: skipping entry with empty id or hashRef",
					slog.String("binding", sb.Metadata.Name))
				continue
			}
			hexHash, err := r.ResolveString(ctx, k.HashRef)
			if err != nil {
				log.Warn("api-key binding: hashRef resolve failed",
					slog.String("binding", sb.Metadata.Name),
					slog.String("id", k.ID),
					slog.String("error", err.Error()))
				continue
			}
			hash, err := DecodeHash(hexHash)
			if err != nil {
				log.Warn("api-key binding: hashRef decode failed",
					slog.String("binding", sb.Metadata.Name),
					slog.String("id", k.ID),
					slog.String("error", err.Error()))
				continue
			}
			groups := k.Groups
			if len(groups) == 0 && sb.Spec.Claims.Groups != "" {
				groups = []string{sb.Spec.Claims.Groups}
			}
			out = append(out, APIKeyBinding{
				ID:      k.ID,
				Hash:    hash,
				Subject: sb.Spec.Claims.SubjectID,
				Issuer:  issuer,
				Email:   sb.Spec.Claims.Email,
				Groups:  append([]string(nil), groups...),
				Claims:  claims,
			})
		}
	}
	return out, nil
}

// apiKeyLiteralClaims turns the ClaimPaths block into a UserCtx.Claims
// map of literal values for the API-key flow. JWT callers treat these
// fields as paths; in API-key mode they are the values to stamp onto the
// authenticated session so row-filter templates like `{{ subject.tenantId }}`
// resolve without a JWT round-trip.
func apiKeyLiteralClaims(c apitypes.ClaimPaths) map[string]any {
	out := make(map[string]any)
	if c.TenantID != "" {
		out["tenantId"] = c.TenantID
	}
	if c.AllowedRegions != "" {
		out["allowedRegions"] = c.AllowedRegions
	}
	for k, v := range c.Extra {
		if k == "" || v == "" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
