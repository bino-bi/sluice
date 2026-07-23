// SPDX-License-Identifier: AGPL-3.0-or-later

package identity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// Sentinel errors from the JWT verifier. All wrap ErrInvalidCredential
// so the middleware maps them uniformly to 401, but preserve context in
// logs.
var (
	ErrJWTBadSignature   = errors.New("identity: jwt bad signature")
	ErrJWTExpired        = errors.New("identity: jwt expired")
	ErrJWTNotYetValid    = errors.New("identity: jwt not yet valid")
	ErrJWTWrongAudience  = errors.New("identity: jwt audience mismatch")
	ErrJWTUnknownIssuer  = errors.New("identity: jwt unknown issuer")
	ErrJWTAlgNotAllowed  = errors.New("identity: jwt alg not allowed")
	ErrJWTMalformed      = errors.New("identity: jwt malformed")
	ErrJWTMissingSubject = errors.New("identity: jwt missing subject")
)

// defaultAllowedAlgs is the MVP algorithm allowlist (concept §MVP-F06).
// alg=none is intentionally absent; HS algorithms are included because
// small self-hosted IdPs still use them.
var defaultAllowedAlgs = []string{"HS256", "HS384", "RS256", "RS384", "ES256", "ES384"}

// JWTOptions configures NewJWTIdentifier. Bindings is required; the
// other fields pick MVP defaults on zero values.
type JWTOptions struct {
	Bindings *BindingRegistry

	// JWKS is the client used to fetch signing keys. When nil, a default
	// client is constructed.
	JWKS *JWKSClient

	// AllowedAlgs restricts the accepted JWT header "alg" values. Empty
	// slice means MVP defaults (HS/RS/ES 256/384).
	AllowedAlgs []string

	// ClockSkew is the per-binding default tolerance on exp/nbf/iat. The
	// binding may override via spec.clockSkew; zero falls through to 60s.
	ClockSkew time.Duration

	// HMACSecrets maps issuer → shared secret used for HS* algorithms.
	// RS/ES keys always come from the JWKS endpoint. HMAC bindings skip
	// the network entirely.
	HMACSecrets map[string][]byte

	Clock  func() time.Time
	Logger *slog.Logger
}

// JWTIdentifier verifies Bearer tokens against per-issuer bindings.
type JWTIdentifier struct {
	opts     JWTOptions
	bindings *BindingRegistry
	jwks     *JWKSClient
	parser   *jwt.Parser
	algSet   map[string]struct{}
	log      *slog.Logger
	clock    func() time.Time

	// hmacSecrets holds the live issuer→secret map; swapped atomically by
	// SetHMACSecrets on config reload while VerifyToken reads lock-free.
	hmacSecrets atomic.Pointer[map[string][]byte]
}

// NewJWTIdentifier constructs the identifier. Requires a non-nil
// BindingRegistry.
func NewJWTIdentifier(opts JWTOptions) (*JWTIdentifier, error) {
	if opts.Bindings == nil {
		return nil, errors.New("identity: JWTIdentifier requires Bindings")
	}
	algs := opts.AllowedAlgs
	if len(algs) == 0 {
		algs = defaultAllowedAlgs
	}
	algSet := make(map[string]struct{}, len(algs))
	for _, a := range algs {
		algSet[a] = struct{}{}
	}
	jwks := opts.JWKS
	if jwks == nil {
		jwks = NewJWKSClient(JWKSClientOptions{Clock: opts.Clock, Logger: opts.Logger})
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}

	parser := jwt.NewParser(
		jwt.WithValidMethods(algs),
		jwt.WithIssuedAt(),
		jwt.WithExpirationRequired(),
	)

	j := &JWTIdentifier{
		opts:     opts,
		bindings: opts.Bindings,
		jwks:     jwks,
		parser:   parser,
		algSet:   algSet,
		log:      log,
		clock:    clock,
	}
	j.hmacSecrets.Store(&opts.HMACSecrets)
	return j, nil
}

// SetHMACSecrets atomically replaces the issuer→secret map. Safe to call
// from any goroutine; concurrent VerifyToken calls see either the old or
// the new map in full. Called by the config-reload subscriber alongside
// BindingRegistry.Apply.
func (j *JWTIdentifier) SetHMACSecrets(secrets map[string][]byte) {
	j.hmacSecrets.Store(&secrets)
}

// Name implements Identifier.
func (*JWTIdentifier) Name() string { return "jwt" }

// Identify extracts and validates a Bearer token from r. Missing header
// returns ErrNoCredential; malformed or unverifiable tokens return a
// wrapped ErrInvalidCredential.
func (j *JWTIdentifier) Identify(ctx context.Context, r *http.Request) (*UserCtx, error) {
	raw := extractBearer(r)
	if raw == "" {
		return nil, ErrNoCredential
	}
	uc, err := j.VerifyToken(ctx, raw)
	if err != nil {
		return nil, err
	}
	uc.RemoteAddr = r.RemoteAddr
	return uc, nil
}

// VerifyToken parses and validates a raw compact JWS. Exposed for MCP
// transports that receive tokens out-of-band.
func (j *JWTIdentifier) VerifyToken(ctx context.Context, raw string) (*UserCtx, error) {
	// First pass: read the header + claims without verifying signature
	// so we can discover the issuer → binding → JWKS URL.
	unverifiedClaims := jwt.MapClaims{}
	preparser := jwt.NewParser(jwt.WithValidMethods(j.allowedAlgs()))
	unverifiedTok, _, err := preparser.ParseUnverified(raw, unverifiedClaims)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrJWTMalformed, err)
	}
	alg, _ := unverifiedTok.Header["alg"].(string)
	if _, ok := j.algSet[alg]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrJWTAlgNotAllowed, alg)
	}
	kid, _ := unverifiedTok.Header["kid"].(string)

	iss, _ := unverifiedClaims["iss"].(string)
	if iss == "" {
		return nil, fmt.Errorf("%w: missing iss", ErrJWTMalformed)
	}
	binding, ok := j.bindings.ForIssuer(iss)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrJWTUnknownIssuer, iss)
	}

	key, err := j.keyFor(ctx, alg, kid, binding)
	if err != nil {
		return nil, err
	}

	// Second pass: verify signature + claims (exp/nbf handled by parser).
	claims := jwt.MapClaims{}
	skew := time.Duration(binding.Spec.ClockSkew)
	if skew <= 0 {
		skew = j.opts.ClockSkew
	}
	if skew <= 0 {
		skew = 60 * time.Second
	}
	parser := jwt.NewParser(
		jwt.WithValidMethods(j.allowedAlgs()),
		jwt.WithLeeway(skew),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(j.clock),
	)
	parsed, err := parser.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		return key, nil
	})
	if err != nil {
		switch {
		case errors.Is(err, jwt.ErrTokenExpired):
			return nil, fmt.Errorf("%w: %w", ErrJWTExpired, err)
		case errors.Is(err, jwt.ErrTokenNotValidYet):
			return nil, fmt.Errorf("%w: %w", ErrJWTNotYetValid, err)
		case errors.Is(err, jwt.ErrTokenSignatureInvalid):
			return nil, fmt.Errorf("%w: %w", ErrJWTBadSignature, err)
		case errors.Is(err, jwt.ErrTokenMalformed):
			return nil, fmt.Errorf("%w: %w", ErrJWTMalformed, err)
		default:
			return nil, fmt.Errorf("%w: %w", ErrInvalidCredential, err)
		}
	}
	if !parsed.Valid {
		return nil, fmt.Errorf("%w: token not valid", ErrInvalidCredential)
	}

	// Audience check: when binding specifies one, at least one aud entry
	// must match. jwt.RegisteredClaims already normalises aud to []string.
	if want := binding.Spec.Audience; want != "" {
		if !audienceMatches(claims, want) {
			return nil, fmt.Errorf("%w: want=%s", ErrJWTWrongAudience, want)
		}
	}

	uc, err := j.buildUserCtx(claims, binding)
	if err != nil {
		return nil, err
	}
	return uc, nil
}

func (j *JWTIdentifier) allowedAlgs() []string {
	out := make([]string, 0, len(j.algSet))
	for a := range j.algSet {
		out = append(out, a)
	}
	return out
}

func (j *JWTIdentifier) keyFor(ctx context.Context, alg, kid string, binding *apitypes.SubjectBinding) (any, error) {
	if strings.HasPrefix(alg, "HS") {
		if m := j.hmacSecrets.Load(); m != nil {
			if secret, ok := (*m)[binding.Spec.Issuer]; ok {
				return secret, nil
			}
		}
		return nil, fmt.Errorf("%w: no HMAC secret for issuer %q", ErrInvalidCredential, binding.Spec.Issuer)
	}
	if binding.Spec.JWKSURL == "" {
		return nil, fmt.Errorf("%w: binding for %q has no jwksUrl", ErrInvalidCredential, binding.Spec.Issuer)
	}
	ttl := time.Duration(binding.Spec.JWKSCacheTTL)
	k, _, err := j.jwks.KeyFor(ctx, binding.Spec.JWKSURL, kid, ttl)
	if err != nil {
		return nil, fmt.Errorf("%w: jwks: %w", ErrInvalidCredential, err)
	}
	return k, nil
}

func (j *JWTIdentifier) buildUserCtx(claims jwt.MapClaims, binding *apitypes.SubjectBinding) (*UserCtx, error) {
	cp := binding.Spec.Claims
	subjectPath := cp.SubjectID
	if subjectPath == "" {
		subjectPath = "sub"
	}
	subject, err := ExtractString(claims, subjectPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrJWTMissingSubject, err)
	}
	if subject == "" {
		return nil, ErrJWTMissingSubject
	}
	var email string
	if cp.Email != "" {
		if v, err := ExtractString(claims, cp.Email); err == nil {
			email = v
		}
	} else if v, ok := claims["email"].(string); ok {
		email = v
	}
	var groups []string
	if cp.Groups != "" {
		if v, err := ExtractStringList(claims, cp.Groups); err == nil {
			groups = v
		}
	}

	authTime := j.clock()
	if iat, ok := claims["iat"].(float64); ok && iat > 0 {
		authTime = time.Unix(int64(iat), 0).UTC()
	}
	iss, _ := claims["iss"].(string)
	// Copy claims into a new map so callers that mutate don't corrupt
	// the parser's internal state.
	copied := maps.Clone(map[string]any(claims))
	if copied == nil {
		copied = make(map[string]any)
	}
	return &UserCtx{
		Subject:    subject,
		Issuer:     iss,
		Email:      email,
		Groups:     groups,
		Claims:     copied,
		AuthMethod: AuthMethodJWT,
		AuthTime:   authTime,
	}, nil
}

// extractBearer pulls a Bearer token out of the Authorization header.
// Empty return means no Bearer credential is present.
func extractBearer(r *http.Request) string {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth == "" {
		return ""
	}
	const prefix = "Bearer "
	if len(auth) <= len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(auth[len(prefix):])
}

// audienceMatches accepts the RFC-7519 quirks: aud may be a string or a
// []string. A match is any element equal to want.
func audienceMatches(claims jwt.MapClaims, want string) bool {
	switch v := claims["aud"].(type) {
	case string:
		return v == want
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == want {
				return true
			}
		}
	case []string:
		return slices.Contains(v, want)
	}
	return false
}
