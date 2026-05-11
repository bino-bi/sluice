// SPDX-License-Identifier: AGPL-3.0-or-later

package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"maps"
	"math/big"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// APIKeyOptions configures an APIKeyIdentifier. Pepper is required —
// a zero-length pepper still authenticates, but the security model
// assumes the pepper is present.
type APIKeyOptions struct {
	// Pepper is a server-wide secret mixed into every HMAC so a leaked
	// HashRef value (e.g., from a config bundle) cannot be used to mint
	// valid keys. Typically resolved from `secret://env/SLUICE_APIKEY_PEPPER`.
	Pepper []byte

	// Bindings maps the presented key ID to its expected HMAC and the
	// identity fields populated on the returned UserCtx. Reload replaces
	// the whole map atomically via SetBindings.
	Bindings []APIKeyBinding

	// Logger receives diagnostic messages. Nil uses slog.Default.
	Logger *slog.Logger

	// Clock returns "now". Nil uses time.Now. Exposed for tests.
	Clock func() time.Time
}

// APIKeyBinding holds the pre-hashed key plus identity metadata. The
// Hash is the HMAC-SHA256 of (keyID + ":" + keyMaterial) computed with
// the server pepper at key issuance time.
type APIKeyBinding struct {
	// ID is the public key ID, prefixed into the presented token
	// ("sl_<env>_<ID>.<material>"). Reported as UserCtx.Subject when
	// Subject is empty.
	ID string

	// Hash is the raw SHA-256 HMAC of the key material (32 bytes).
	Hash []byte

	// Subject overrides UserCtx.Subject. When empty, ID is used.
	Subject string

	// Issuer goes into UserCtx.Issuer, usually "sluice-apikeys" or the
	// admin audit source.
	Issuer string

	// Email optional — copied into UserCtx.Email.
	Email string

	// Groups populates UserCtx.Groups for policy matchers.
	Groups []string

	// Claims are literal key/value pairs copied into UserCtx.Claims. The
	// row-filter templating reads fields such as "tenantId" from this map.
	Claims map[string]any
}

// APIKeyIdentifier verifies inbound API keys. It matches the
// "sl_<env>_<keyid>.<base62_material>" format — the prefix is not
// validated beyond presence of a dot; the keyID before the dot keys the
// Bindings map for O(1) lookup.
type APIKeyIdentifier struct {
	opts APIKeyOptions
	log  *slog.Logger

	// index is swapped atomically on reload so the request path is
	// lock-free while SetBindings can replace the whole map.
	index atomic.Pointer[map[string]*APIKeyBinding]
}

// NewAPIKeyIdentifier constructs an APIKeyIdentifier. A nil opts is
// treated as APIKeyOptions{}; no bindings configured means every call
// returns ErrNoCredential.
func NewAPIKeyIdentifier(opts APIKeyOptions) *APIKeyIdentifier {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	a := &APIKeyIdentifier{
		opts: opts,
		log:  opts.Logger,
	}
	a.index.Store(buildAPIKeyIndex(opts.Bindings))
	return a
}

// SetBindings atomically replaces the binding table. Safe to call from
// any goroutine; concurrent Identify calls see either the old or the new
// table in full (never a half-applied state).
func (a *APIKeyIdentifier) SetBindings(bindings []APIKeyBinding) {
	a.index.Store(buildAPIKeyIndex(bindings))
}

// buildAPIKeyIndex materialises a lookup map from a flat binding slice.
// Bindings with an empty ID or empty hash are skipped — they can never
// match and would only bloat the map.
func buildAPIKeyIndex(bindings []APIKeyBinding) *map[string]*APIKeyBinding {
	idx := make(map[string]*APIKeyBinding, len(bindings))
	for i := range bindings {
		b := bindings[i]
		if b.ID == "" || len(b.Hash) == 0 {
			continue
		}
		idx[b.ID] = &b
	}
	return &idx
}

// Name implements Identifier.
func (*APIKeyIdentifier) Name() string { return "api_key" }

// Identify extracts an API key from Authorization: ApiKey … or
// X-Api-Key, verifies its HMAC, and returns a UserCtx. Missing key
// returns ErrNoCredential; any other validation failure returns
// ErrInvalidCredential with a constant-time compare + small random
// delay to muddy timing attacks.
func (a *APIKeyIdentifier) Identify(_ context.Context, r *http.Request) (*UserCtx, error) {
	raw := extractAPIKey(r)
	if raw == "" {
		return nil, ErrNoCredential
	}
	keyID, material := splitAPIKey(raw)
	if keyID == "" || material == "" {
		a.timingJitter()
		return nil, fmt.Errorf("%w: malformed key", ErrInvalidCredential)
	}

	idx := a.index.Load()
	var b *APIKeyBinding
	if idx != nil {
		b = (*idx)[keyID]
	}
	if b == nil {
		a.timingJitter()
		return nil, fmt.Errorf("%w: unknown key id", ErrInvalidCredential)
	}
	want := b.Hash
	got := hmacSum(a.opts.Pepper, keyID, material)
	if !hmac.Equal(want, got) {
		a.timingJitter()
		return nil, fmt.Errorf("%w: bad hmac", ErrInvalidCredential)
	}

	subject := b.Subject
	if subject == "" {
		subject = b.ID
	}
	var claims map[string]any
	if len(b.Claims) > 0 {
		claims = make(map[string]any, len(b.Claims))
		maps.Copy(claims, b.Claims)
	}
	u := &UserCtx{
		Subject:    subject,
		Issuer:     firstNonEmpty(b.Issuer, "sluice-apikeys"),
		Email:      b.Email,
		Groups:     append([]string(nil), b.Groups...),
		Claims:     claims,
		AuthMethod: AuthMethodAPIKey,
		AuthTime:   a.opts.Clock(),
		RemoteAddr: r.RemoteAddr,
	}
	return u, nil
}

// extractAPIKey pulls the raw key out of the request headers. Supports
// two conventional patterns:
//
//   - Authorization: ApiKey sl_prod_abc.material
//   - X-Api-Key: sl_prod_abc.material
func extractAPIKey(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Api-Key")); v != "" {
		return v
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth == "" {
		return ""
	}
	const prefix = "ApiKey "
	if len(auth) > len(prefix) && strings.EqualFold(auth[:len(prefix)], prefix) {
		return strings.TrimSpace(auth[len(prefix):])
	}
	return ""
}

// splitAPIKey separates keyID and key material. Input shape:
//
//	sl_<env>_<id>.<material>
//
// Accepts any prefix; splits on the last "." so the id segment may
// contain underscores. Returns "","" when the format is invalid.
func splitAPIKey(raw string) (string, string) {
	dot := strings.LastIndex(raw, ".")
	if dot <= 0 || dot == len(raw)-1 {
		return "", ""
	}
	return raw[:dot], raw[dot+1:]
}

// hmacSum computes HMAC-SHA256 over (keyID + ":" + material) using the
// server pepper as the key. Deterministic so offline key issuance can
// precompute the same value.
func hmacSum(pepper []byte, keyID, material string) []byte {
	h := hmac.New(sha256.New, pepper)
	h.Write([]byte(keyID))
	h.Write([]byte{':'})
	h.Write([]byte(material))
	return h.Sum(nil)
}

// ComputeHash is exposed so the `sluice apikey hash` CLI command can
// emit the value an operator stores in secret://env/SLUICE_APIKEY_<ID>.
// Not used on the request path.
func ComputeHash(pepper []byte, keyID, material string) []byte {
	return hmacSum(pepper, keyID, material)
}

// DecodeHash parses a hex-encoded HMAC produced by ComputeHash. Callers
// that store hashes as hex in config use this adapter.
func DecodeHash(hexHash string) ([]byte, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(hexHash))
	if err != nil {
		return nil, fmt.Errorf("identity: decode hash: %w", err)
	}
	if len(raw) != sha256.Size {
		return nil, fmt.Errorf("identity: hash length %d; want %d", len(raw), sha256.Size)
	}
	return raw, nil
}

// timingJitter sleeps for a short random interval on failure so response
// time does not obviously leak "unknown key id" vs "bad hmac". Bounded
// [1ms, 5ms); negligible on a legitimate path (which returns before we
// get here).
func (a *APIKeyIdentifier) timingJitter() {
	// Using math/big with crypto/rand avoids the global seed and is
	// constant-time at the read.
	n, err := rand.Int(rand.Reader, big.NewInt(4_000_000)) // 4ms range in ns
	if err != nil {
		n = big.NewInt(1_500_000)
	}
	time.Sleep(time.Duration(1_000_000 + n.Int64()))
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
