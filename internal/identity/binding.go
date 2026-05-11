// SPDX-License-Identifier: AGPL-3.0-or-later

package identity

import (
	"fmt"
	"slices"
	"sync/atomic"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// BindingRegistry indexes SubjectBindings by issuer so the JWT verifier
// can resolve the appropriate JWKS / audience / claim paths given the
// incoming token's "iss" claim. The registry is goroutine-safe and swaps
// the whole binding set atomically on config reload.
type BindingRegistry struct {
	snapshot atomic.Pointer[bindingSnapshot]
}

type bindingSnapshot struct {
	all      []apitypes.SubjectBinding
	byIssuer map[string]*apitypes.SubjectBinding
}

// NewBindingRegistry builds a registry from the initial binding list.
// Returns an error if two bindings declare the same non-empty issuer —
// that shape is ambiguous on token lookup. API-only bindings (no issuer)
// are allowed; they just aren't reachable via ForIssuer.
func NewBindingRegistry(initial []apitypes.SubjectBinding) (*BindingRegistry, error) {
	r := &BindingRegistry{}
	snap, err := buildSnapshot(initial)
	if err != nil {
		return nil, err
	}
	r.snapshot.Store(snap)
	return r, nil
}

// Apply atomically replaces the binding set. Called when the config
// registry publishes a new Snapshot.
func (r *BindingRegistry) Apply(next []apitypes.SubjectBinding) error {
	snap, err := buildSnapshot(next)
	if err != nil {
		return err
	}
	r.snapshot.Store(snap)
	return nil
}

// ForIssuer returns the binding whose spec.issuer matches iss, or (nil,
// false) when no binding claims that issuer.
func (r *BindingRegistry) ForIssuer(iss string) (*apitypes.SubjectBinding, bool) {
	snap := r.snapshot.Load()
	if snap == nil {
		return nil, false
	}
	b, ok := snap.byIssuer[iss]
	return b, ok
}

// All returns a defensive copy of the binding set.
func (r *BindingRegistry) All() []apitypes.SubjectBinding {
	snap := r.snapshot.Load()
	if snap == nil {
		return nil
	}
	return slices.Clone(snap.all)
}

// Issuers returns the issuers currently indexed.
func (r *BindingRegistry) Issuers() []string {
	snap := r.snapshot.Load()
	if snap == nil {
		return nil
	}
	out := make([]string, 0, len(snap.byIssuer))
	for iss := range snap.byIssuer {
		out = append(out, iss)
	}
	slices.Sort(out)
	return out
}

func buildSnapshot(list []apitypes.SubjectBinding) (*bindingSnapshot, error) {
	byIssuer := make(map[string]*apitypes.SubjectBinding, len(list))
	all := slices.Clone(list)
	for i := range all {
		b := &all[i]
		iss := b.Spec.Issuer
		if iss == "" {
			continue
		}
		if _, dup := byIssuer[iss]; dup {
			return nil, fmt.Errorf("identity: duplicate SubjectBinding for issuer %q", iss)
		}
		byIssuer[iss] = b
	}
	return &bindingSnapshot{all: all, byIssuer: byIssuer}, nil
}
