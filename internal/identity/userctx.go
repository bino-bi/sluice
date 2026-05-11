// SPDX-License-Identifier: AGPL-3.0-or-later

package identity

import (
	"context"
	"maps"
	"slices"
	"time"
)

// AuthMethod identifies how the request was authenticated. Stored on the
// UserCtx and copied into audit records for forensics.
type AuthMethod string

// Auth methods recognised by the MVP. OIDC + mTLS are v1 additions.
const (
	AuthMethodJWT    AuthMethod = "jwt"
	AuthMethodAPIKey AuthMethod = "api_key"
	AuthMethodAdmin  AuthMethod = "admin_token"
	AuthMethodNone   AuthMethod = "none"
)

// UserCtx carries the authenticated subject across the request pipeline.
// It is passed by value inside contexts so mutations to one request's
// UserCtx cannot leak into another request.
type UserCtx struct {
	// Subject is the stable identifier (sub claim / API-key ID) of the
	// authenticated principal. Required.
	Subject string

	// Issuer is the IdP that minted the credential. Empty for API keys
	// unless the binding overrides it.
	Issuer string

	// Email is the well-known email claim, when present.
	Email string

	// Groups are the coarse-grained roles a policy matcher keys off.
	Groups []string

	// Claims retains every raw JWT claim so CEL-based row filters can
	// reach additional fields (tenant_id, allowed_regions, …). Nil for
	// API-key auth unless the SubjectBinding populates Extras.
	Claims map[string]any

	// AuthMethod identifies how the subject was authenticated.
	AuthMethod AuthMethod

	// AuthTime is when the credential was minted (iat) or verified
	// (API-key resolve time). Used in audit records.
	AuthTime time.Time

	// RequestID is the transport-layer request identifier (ULID). The
	// middleware populates this from the incoming trace header or mints
	// a new ULID if none is present.
	RequestID string

	// RemoteAddr is the observed client address at the transport layer.
	// Populated by middleware; may be empty for non-HTTP transports.
	RemoteAddr string
}

// HasGroup reports whether the user is a member of group. The lookup is
// case-sensitive by design — groups should be normalized by the
// SubjectBinding so policy authors do not fight casing.
func (u *UserCtx) HasGroup(group string) bool {
	if u == nil {
		return false
	}
	return slices.Contains(u.Groups, group)
}

// Clone returns a deep copy so downstream code can mutate freely. Groups
// and Claims are copied; other fields are value types.
func (u *UserCtx) Clone() *UserCtx {
	if u == nil {
		return nil
	}
	out := *u
	if u.Groups != nil {
		out.Groups = slices.Clone(u.Groups)
	}
	if u.Claims != nil {
		out.Claims = maps.Clone(u.Claims)
	}
	return &out
}

// userCtxKey is the private context key used for UserCtx storage.
// Exported via WithUser / FromContext.
type userCtxKey struct{}

// WithUser stores u on ctx. Passing nil removes any existing UserCtx.
func WithUser(ctx context.Context, u *UserCtx) context.Context {
	if u == nil {
		return context.WithValue(ctx, userCtxKey{}, (*UserCtx)(nil))
	}
	return context.WithValue(ctx, userCtxKey{}, u)
}

// FromContext returns the UserCtx stored on ctx and whether one was
// present. Missing or nil UserCtx returns (nil, false).
func FromContext(ctx context.Context) (*UserCtx, bool) {
	u, ok := ctx.Value(userCtxKey{}).(*UserCtx)
	if !ok || u == nil {
		return nil, false
	}
	return u, true
}

// MustFromContext returns the UserCtx or panics. Use only in code paths
// where the middleware guarantees presence (e.g., after authn succeeds).
func MustFromContext(ctx context.Context) *UserCtx {
	u, ok := FromContext(ctx)
	if !ok {
		panic("identity: no UserCtx on context — missing middleware?")
	}
	return u
}
