// SPDX-License-Identifier: AGPL-3.0-or-later

// Package policycache memoises the (Decision, RewriteResult) pair for a
// given SQL text + identity under a fixed policy snapshot. It removes the
// parse→evaluate→rewrite cost from repeated identical queries; rate
// limiting, budgets, approval, and audit still run per request because the
// cache sits at the evaluate/rewrite boundary only.
package policycache

import (
	"crypto/sha256"
	"encoding/json"
	"sort"
	"time"

	lru "github.com/hashicorp/golang-lru/v2/expirable"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/rewriter"
)

// Key identifies a cached decision. It binds the exact SQL text and the
// full identity to a specific policy snapshot; a snapshot publish makes
// every prior key unreachable (Version + Digest change).
type Key struct {
	Version      int64
	Digest       string
	SQLHash      [32]byte
	IdentityHash [32]byte
}

// Entry is the cached result. Both fields are treated as immutable by
// callers — the cache hands back shared pointers.
type Entry struct {
	Decision *policy.Decision
	Rewrite  *rewriter.RewriteResult
}

// Cache is a TTL-bounded LRU of decisions. It is safe for concurrent use.
type Cache struct {
	lru *lru.LRU[Key, *Entry]
}

// New returns a cache holding up to size entries, each expiring after ttl.
func New(size int, ttl time.Duration) *Cache {
	if size <= 0 {
		size = 4096
	}
	return &Cache{lru: lru.NewLRU[Key, *Entry](size, nil, ttl)}
}

// Get returns the cached entry for k, if present and unexpired.
func (c *Cache) Get(k Key) (*Entry, bool) {
	if c == nil || c.lru == nil {
		return nil, false
	}
	return c.lru.Get(k)
}

// Put stores e under k.
func (c *Cache) Put(k Key, e *Entry) {
	if c == nil || c.lru == nil {
		return
	}
	c.lru.Add(k, e)
}

// Purge drops every entry. Called on snapshot reload to release memory
// promptly (stale entries are already unreachable by key).
func (c *Cache) Purge() {
	if c == nil || c.lru == nil {
		return
	}
	c.lru.Purge()
}

// BuildKey computes the cache key. SQLHash is sha256 of the RAW SQL text —
// deliberately NOT the pg_query fingerprint, which normalises literals:
// two queries differing only in a WHERE value share a fingerprint, and a
// fingerprint-keyed cache would replay the first query's baked-in literals
// for the second. IdentityHash covers every input a selector, template, or
// CEL expression can read.
func BuildKey(version int64, digest, sql string, user *identity.UserCtx, facts *policy.RequestFacts, keyHeaders []string, allHeaders bool) Key {
	return Key{
		Version:      version,
		Digest:       digest,
		SQLHash:      sha256.Sum256([]byte(sql)),
		IdentityHash: identityHash(user, facts, keyHeaders, allHeaders),
	}
}

func identityHash(user *identity.UserCtx, facts *policy.RequestFacts, keyHeaders []string, allHeaders bool) [32]byte {
	payload := struct {
		Subject    string            `json:"subject"`
		Issuer     string            `json:"issuer"`
		Email      string            `json:"email"`
		AuthMethod string            `json:"auth_method"`
		Groups     []string          `json:"groups"`
		Claims     map[string]any    `json:"claims"`
		RemoteIP   string            `json:"remote_ip"`
		UserAgent  string            `json:"user_agent"`
		Headers    map[string]string `json:"headers"`
	}{
		Headers: map[string]string{},
	}
	if user != nil {
		payload.Subject = user.Subject
		payload.Issuer = user.Issuer
		payload.Email = user.Email
		payload.AuthMethod = string(user.AuthMethod)
		payload.Groups = append([]string(nil), user.Groups...)
		sort.Strings(payload.Groups)
		payload.Claims = user.Claims
	}
	if facts != nil {
		if facts.RemoteIP != nil {
			payload.RemoteIP = facts.RemoteIP.String()
		}
		payload.UserAgent = facts.UserAgent
		if allHeaders {
			for k, v := range facts.Headers {
				payload.Headers[k] = v
			}
		} else {
			for _, h := range keyHeaders {
				if v, ok := facts.Headers[h]; ok {
					payload.Headers[h] = v
				}
			}
		}
	}
	// json.Marshal sorts map keys, so the encoding is deterministic.
	b, _ := json.Marshal(payload)
	return sha256.Sum256(b)
}
