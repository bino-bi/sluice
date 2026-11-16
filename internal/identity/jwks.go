// SPDX-License-Identifier: AGPL-3.0-or-later

package identity

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// Sentinel errors from the JWKS cache. Wrapped; callers should use
// errors.Is.
var (
	ErrJWKSUnreachable = errors.New("identity: jwks endpoint unreachable")
	ErrJWKSMalformed   = errors.New("identity: jwks payload malformed")
	ErrKeyNotFound     = errors.New("identity: signing key not found")
	ErrUnsupportedKey  = errors.New("identity: unsupported jwk key type")
)

// MaxJWKSStaleAge is how long after its cache time a key set is still
// served while refreshes fail. After that the issuer fails closed, so a
// key the IdP removed does not stay valid while its JWKS URL is blocked.
const MaxJWKSStaleAge = 24 * time.Hour

// JWKSClient fetches and caches JSON Web Key Sets. One client instance
// serves many issuers — it maintains per-issuer state keyed on the JWKS
// URL. TTL controls stale age; an unknown kid triggers a single refresh
// (coalesced under a per-URL lock) before giving up. A failed refresh
// blocks the next one for minRefresh, and stale keys are served in the
// meantime for at most MaxJWKSStaleAge.
type JWKSClient struct {
	http       *http.Client
	defaultTTL time.Duration
	minRefresh time.Duration // floor between unknown-kid refreshes and after a failed refresh
	clock      func() time.Time
	log        *slog.Logger
	urlMu      sync.Mutex
	urlLocks   map[string]chan struct{} // a lock that a waiter can leave on ctx.Done
	cache      sync.Map                 // url → *jwksEntry
}

// JWKSClientOptions tunes the client. Zero values pick defaults.
type JWKSClientOptions struct {
	HTTPClient *http.Client  // default: &http.Client{Timeout: 5s}
	DefaultTTL time.Duration // default: 10m
	MinRefresh time.Duration // default: 30s — floor between unknown-kid refreshes and after a failed refresh
	Clock      func() time.Time
	Logger     *slog.Logger
}

// NewJWKSClient constructs a JWKS cache. Instances are safe for use by
// many goroutines.
func NewJWKSClient(opts JWKSClientOptions) *JWKSClient {
	c := &JWKSClient{
		http:       opts.HTTPClient,
		defaultTTL: opts.DefaultTTL,
		minRefresh: opts.MinRefresh,
		clock:      opts.Clock,
		log:        opts.Logger,
		urlLocks:   make(map[string]chan struct{}),
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: 5 * time.Second}
	}
	if c.defaultTTL <= 0 {
		c.defaultTTL = 10 * time.Minute
	}
	if c.minRefresh <= 0 {
		c.minRefresh = 30 * time.Second
	}
	if c.clock == nil {
		c.clock = time.Now
	}
	if c.log == nil {
		c.log = slog.Default()
	}
	return c
}

// KeyFor returns the crypto public key for the given kid from the JWKS
// at url. When the cached entry is stale, or the kid is unknown, the
// client fetches and retries once. Unknown kid after refresh returns
// ErrKeyNotFound.
func (c *JWKSClient) KeyFor(ctx context.Context, url, kid string, ttl time.Duration) (any, string, error) {
	if url == "" {
		return nil, "", fmt.Errorf("%w: empty url", ErrJWKSUnreachable)
	}
	useTTL := ttl
	if useTTL <= 0 {
		useTTL = c.defaultTTL
	}

	// Fast path: cache hit + known kid.
	if entry, ok := c.load(url); ok && !c.stale(entry, useTTL) {
		if k, alg, ok := entry.lookup(kid); ok {
			return k, alg, nil
		}
	}

	// Refresh under a per-URL lock so concurrent unknown-kid misses
	// collapse into a single HTTP round-trip. While another request
	// refreshes, a usable stale key is served at once instead.
	lock := c.lockFor(url)
	select {
	case lock <- struct{}{}:
	default:
		if prev, ok := c.load(url); ok {
			if k, alg, ok := c.staleLookup(prev, kid, useTTL); ok {
				return k, alg, nil
			}
		}
		select {
		case lock <- struct{}{}:
		case <-ctx.Done():
			return nil, "", fmt.Errorf("%w: %w", ErrJWKSUnreachable, ctx.Err())
		}
	}
	defer func() { <-lock }()

	// Track whether this refresh is driven by an unknown-kid miss vs a
	// TTL expiry so the rate-limit only applies to the former.
	var missDriven bool
	if entry, ok := c.load(url); ok {
		if !c.stale(entry, useTTL) {
			if k, alg, ok := entry.lookup(kid); ok {
				return k, alg, nil
			}
			missDriven = true
			// Fresh cache + unknown kid: apply the rate-limit so repeated
			// attacks don't hammer the JWKS endpoint. The first miss after
			// a TTL-driven fetch is always allowed through.
			if c.recent(entry.refreshedForMissAt) || c.recent(entry.failedAt) {
				return nil, "", fmt.Errorf("%w: kid=%s (refresh rate-limited)", ErrKeyNotFound, kid)
			}
		} else if c.recent(entry.failedAt) {
			// The last refresh failed a moment ago: no new fetch yet.
			if k, alg, ok := c.staleLookup(entry, kid, useTTL); ok {
				return k, alg, nil
			}
			return nil, "", fmt.Errorf("%w: the last refresh failed, next try after %s", ErrJWKSUnreachable, c.minRefresh)
		}
	}

	entry, err := c.fetch(ctx, url)
	if err != nil {
		// A client that left says nothing about the endpoint. A deadline
		// does: a hanging endpoint must be backed off too.
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, "", err
		}
		// Record the failure so the next requests do not fetch again. An
		// empty entry is the negative cache when nothing was fetched yet.
		next := &jwksEntry{}
		prev, hadPrev := c.load(url)
		if hadPrev {
			cp := *prev
			next = &cp
		}
		next.failedAt = c.clock()
		c.cache.Store(url, next)
		// Serve a stale entry on transient failures so one flaky JWKS
		// fetch doesn't throw out every request.
		if hadPrev {
			if k, alg, ok := c.staleLookup(prev, kid, useTTL); ok {
				c.log.WarnContext(ctx, "identity: jwks refresh failed, serving stale",
					slog.String("url", url),
					slog.String("error", err.Error()),
				)
				return k, alg, nil
			}
		}
		return nil, "", err
	}
	if missDriven {
		entry.refreshedForMissAt = c.clock()
	}
	c.cache.Store(url, entry)

	if k, alg, ok := entry.lookup(kid); ok {
		return k, alg, nil
	}
	return nil, "", fmt.Errorf("%w: kid=%s", ErrKeyNotFound, kid)
}

// staleLookup finds kid in e unless the set is more than MaxJWKSStaleAge
// past its cache time. A fresh entry always qualifies.
func (c *JWKSClient) staleLookup(e *jwksEntry, kid string, ttl time.Duration) (any, string, bool) {
	if e.fetchedAt.IsZero() || c.clock().Sub(e.fetchedAt) > ttl+MaxJWKSStaleAge {
		return nil, "", false
	}
	return e.lookup(kid)
}

// recent reports whether t is set and less than minRefresh ago.
func (c *JWKSClient) recent(t time.Time) bool {
	return !t.IsZero() && c.clock().Sub(t) < c.minRefresh
}

// Invalidate drops the cache entry for url so the next KeyFor forces a
// fresh fetch. Exposed for tests and admin /admin/reload.
func (c *JWKSClient) Invalidate(url string) {
	c.cache.Delete(url)
}

func (c *JWKSClient) load(url string) (*jwksEntry, bool) {
	v, ok := c.cache.Load(url)
	if !ok {
		return nil, false
	}
	return v.(*jwksEntry), true
}

func (c *JWKSClient) stale(e *jwksEntry, ttl time.Duration) bool {
	return c.clock().Sub(e.fetchedAt) > ttl
}

func (c *JWKSClient) lockFor(url string) chan struct{} {
	c.urlMu.Lock()
	defer c.urlMu.Unlock()
	lock, ok := c.urlLocks[url]
	if !ok {
		lock = make(chan struct{}, 1)
		c.urlLocks[url] = lock
	}
	return lock
}

func (c *JWKSClient) fetch(ctx context.Context, url string) (*jwksEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrJWKSUnreachable, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrJWKSUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%w: HTTP %d", ErrJWKSUnreachable, resp.StatusCode)
	}
	const maxBody = 1 << 20 // 1 MiB — plenty for a JWKS, stops DoS via huge payloads.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrJWKSUnreachable, err)
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("%w: body > %d bytes", ErrJWKSMalformed, maxBody)
	}
	entry, err := parseJWKS(body)
	if err != nil {
		return nil, err
	}
	entry.fetchedAt = c.clock()
	return entry, nil
}

// jwksEntry is the parsed, cache-ready form of a JWKS document.
type jwksEntry struct {
	keys      map[string]jwkParsed
	fetchedAt time.Time
	// refreshedForMissAt is set when an unknown-kid miss triggered the
	// fetch that produced this entry. It paces repeated unknown-kid
	// attempts so a malicious client can't hammer the JWKS endpoint.
	refreshedForMissAt time.Time
	// failedAt is the time of the last failed refresh. It blocks the next
	// fetch for minRefresh. An entry with only failedAt holds no keys.
	failedAt time.Time
}

type jwkParsed struct {
	pub any    // *rsa.PublicKey | *ecdsa.PublicKey | []byte (HMAC)
	alg string // "RS256", "ES256", "HS256", …
}

func (e *jwksEntry) lookup(kid string) (any, string, bool) {
	// When kid is empty (some IdPs omit it when only one key is live),
	// return the sole key if there is exactly one — otherwise refuse so
	// we don't silently pick the wrong key.
	if kid == "" {
		if len(e.keys) != 1 {
			return nil, "", false
		}
		for _, v := range e.keys {
			return v.pub, v.alg, true
		}
	}
	k, ok := e.keys[kid]
	if !ok {
		return nil, "", false
	}
	return k.pub, k.alg, true
}

// jwkDoc mirrors the subset of RFC 7517 we consume.
type jwkDoc struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`

	// RSA
	N string `json:"n"`
	E string `json:"e"`

	// EC
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`

	// Symmetric (HS256 etc.) — rare in JWKS but supported.
	K string `json:"k"`
}

func parseJWKS(body []byte) (*jwksEntry, error) {
	var doc jwkDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrJWKSMalformed, err)
	}
	entry := &jwksEntry{keys: make(map[string]jwkParsed, len(doc.Keys))}
	for i, k := range doc.Keys {
		if k.Use != "" && k.Use != "sig" {
			// Only signing keys are interesting here.
			continue
		}
		pub, err := parseJWK(k)
		if err != nil {
			return nil, fmt.Errorf("%w: key %d: %w", ErrJWKSMalformed, i, err)
		}
		kid := k.Kid
		if kid == "" {
			// JWKSentries without a kid are addressable by their alg
			// alone only when the set is single-key; we still store them
			// under a synthetic id to keep len() correct.
			kid = fmt.Sprintf("__kidless_%d__", i)
		}
		entry.keys[kid] = jwkParsed{pub: pub, alg: k.Alg}
	}
	return entry, nil
}

func parseJWK(k jwkKey) (any, error) {
	switch k.Kty {
	case "RSA":
		return parseRSAJWK(k)
	case "EC":
		return parseECJWK(k)
	case "oct":
		raw, err := base64URLDecode(k.K)
		if err != nil {
			return nil, fmt.Errorf("invalid oct k: %w", err)
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("%w: kty=%q", ErrUnsupportedKey, k.Kty)
	}
}

func parseRSAJWK(k jwkKey) (*rsa.PublicKey, error) {
	nBytes, err := base64URLDecode(k.N)
	if err != nil {
		return nil, fmt.Errorf("invalid RSA n: %w", err)
	}
	eBytes, err := base64URLDecode(k.E)
	if err != nil {
		return nil, fmt.Errorf("invalid RSA e: %w", err)
	}
	if len(nBytes) == 0 || len(eBytes) == 0 {
		return nil, errors.New("RSA key n or e empty")
	}
	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)
	if !e.IsInt64() || e.Int64() <= 0 {
		return nil, fmt.Errorf("RSA exponent out of range: %s", e.String())
	}
	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}

func parseECJWK(k jwkKey) (*ecdsa.PublicKey, error) {
	var curve elliptic.Curve
	switch k.Crv {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	case "P-521":
		curve = elliptic.P521()
	default:
		return nil, fmt.Errorf("%w: crv=%q", ErrUnsupportedKey, k.Crv)
	}
	xBytes, err := base64URLDecode(k.X)
	if err != nil {
		return nil, fmt.Errorf("invalid EC x: %w", err)
	}
	yBytes, err := base64URLDecode(k.Y)
	if err != nil {
		return nil, fmt.Errorf("invalid EC y: %w", err)
	}
	// Coordinate widths must match the curve byte size; otherwise the
	// point is bogus.
	size := (curve.Params().BitSize + 7) / 8
	if len(xBytes) > size || len(yBytes) > size {
		return nil, errors.New("EC coordinates exceed curve size")
	}
	// SEC 1 uncompressed point: 0x04 || X || Y, each left-padded to the
	// curve size. Parsing it also rejects a point that is not on the curve.
	point := make([]byte, 1+2*size)
	point[0] = 4
	copy(point[1+size-len(xBytes):], xBytes)
	copy(point[1+2*size-len(yBytes):], yBytes)
	pub, err := ecdsa.ParseUncompressedPublicKey(curve, point)
	if err != nil {
		return nil, fmt.Errorf("invalid EC point: %w", err)
	}
	return pub, nil
}

// base64URLDecode decodes RFC 7515 base64url with or without padding.
func base64URLDecode(s string) ([]byte, error) {
	if s == "" {
		return nil, errors.New("empty base64url input")
	}
	// JWK values use un-padded base64url but tolerate both forms.
	if padLen := len(s) % 4; padLen != 0 {
		s = s + "===="[padLen:]
	}
	return base64.URLEncoding.DecodeString(s)
}
