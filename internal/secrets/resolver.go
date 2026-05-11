// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Provider fetches a secret's raw bytes for a specific URI scheme (env, file, …).
// Providers are expected to be stateless and safe for concurrent use.
type Provider interface {
	Scheme() string
	Fetch(ctx context.Context, u URI) ([]byte, error)
}

// Resolver maps `secret://` URIs to raw byte blobs with caching and atomic
// invalidation. Resolver is safe for concurrent use.
type Resolver struct {
	cache     Cache
	ttl       time.Duration
	logger    *slog.Logger
	providers map[string]Provider
}

// ResolverOptions configures a Resolver. Zero values are filled with sensible
// defaults; callers only override what they need.
type ResolverOptions struct {
	Cache     Cache
	TTL       time.Duration
	Logger    *slog.Logger
	Providers []Provider // override / extend the default (env + file) set
}

// NewResolver builds a Resolver. When opts.Providers is nil the default set
// (env, file) is registered. The file provider uses opts.Logger for
// world-readable-file warnings.
func NewResolver(opts ResolverOptions) *Resolver {
	if opts.Cache == nil {
		opts.Cache = DefaultCache(1000, 10*time.Minute)
	}
	if opts.TTL <= 0 {
		opts.TTL = 10 * time.Minute
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	r := &Resolver{
		cache:     opts.Cache,
		ttl:       opts.TTL,
		logger:    opts.Logger,
		providers: make(map[string]Provider),
	}

	if len(opts.Providers) == 0 {
		r.providers["env"] = envProvider{}
		r.providers["file"] = fileProvider{logger: opts.Logger}
	} else {
		for _, p := range opts.Providers {
			r.providers[p.Scheme()] = p
		}
	}
	return r
}

// Resolve returns the raw bytes referenced by uri. Successive calls for the
// same uri return cached bytes until TTL elapses or Invalidate is called.
// The returned slice is owned by the caller; the resolver keeps a separate
// copy in the cache.
func (r *Resolver) Resolve(ctx context.Context, uri string) ([]byte, error) {
	if cached, ok := r.cache.Get(uri); ok {
		return cached, nil
	}

	u, err := Parse(uri)
	if err != nil {
		return nil, err
	}
	p, ok := r.providers[u.Provider]
	if !ok {
		return nil, fmt.Errorf("secrets: unsupported provider %q", u.Provider)
	}

	val, err := p.Fetch(ctx, u)
	if err != nil {
		return nil, err
	}

	r.cache.Set(uri, val, r.ttl)
	// Defensive copy: callers should not observe the same backing array as
	// the cache layer stored.
	out := make([]byte, len(val))
	copy(out, val)
	return out, nil
}

// ResolveString is sugar for string-typed secrets. Trailing whitespace
// (newlines, spaces) is stripped so `printf "value\n" > secret` works.
func (r *Resolver) ResolveString(ctx context.Context, uri string) (string, error) {
	b, err := r.Resolve(ctx, uri)
	if err != nil {
		return "", err
	}
	return string(bytes.TrimRight(b, " \t\r\n")), nil
}

// Invalidate drops every cached value. Called after a successful policy
// reload, since secret references may have changed.
func (r *Resolver) Invalidate() {
	r.cache.InvalidateAll()
}

// Close releases resolver-owned resources. Currently a no-op; v1 Vault
// provider uses it to stop lease-renewal goroutines.
func (r *Resolver) Close() error {
	r.cache.InvalidateAll()
	return nil
}
