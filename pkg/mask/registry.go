// SPDX-License-Identifier: Apache-2.0

package mask

import (
	"maps"
	"sort"
	"sync"
	"sync/atomic"
)

// Registry is a lookup table from mask-type string to Provider. Readers
// take an atomic snapshot; writers copy-on-write under a mutex. This keeps
// the hot query path lock-free.
type Registry struct {
	mu   sync.Mutex
	snap atomic.Pointer[map[string]Provider]
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	r := &Registry{}
	empty := map[string]Provider{}
	r.snap.Store(&empty)
	return r
}

// Register adds a provider to the registry. It returns ErrDuplicateType if
// a provider with the same Type() is already registered — this is a
// configuration bug and should surface at startup.
func (r *Registry) Register(p Provider) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur := *r.snap.Load()
	if _, exists := cur[p.Type()]; exists {
		return ErrDuplicateType
	}
	next := maps.Clone(cur)
	next[p.Type()] = p
	r.snap.Store(&next)
	return nil
}

// Lookup returns the provider for a mask type, or (nil, false).
func (r *Registry) Lookup(maskType string) (Provider, bool) {
	p, ok := (*r.snap.Load())[maskType]
	return p, ok
}

// Types returns the sorted list of registered mask-type strings.
func (r *Registry) Types() []string {
	m := *r.snap.Load()
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// defaultOnce guards the process-wide registry.
var (
	defaultOnce sync.Once
	defaultReg  *Registry
)

// Default returns the process-wide Registry with the built-in providers
// registered. The first call constructs it; subsequent calls return the
// same pointer.
func Default() *Registry {
	defaultOnce.Do(func() {
		defaultReg = NewRegistry()
		for _, p := range builtins() {
			if err := defaultReg.Register(p); err != nil {
				// builtins() is hand-curated; a collision here is a
				// programming error, not a runtime condition.
				panic("mask: builtin provider registration failed: " + err.Error())
			}
		}
	})
	return defaultReg
}

// builtins returns the set of providers registered by Default().
func builtins() []Provider {
	return []Provider{
		newNullProvider(),
		newConstantProvider(),
		newPartialProvider(),
		newHashProvider(),
		newRegexProvider(),
		newTruncateProvider(),
		newFPEProvider(),
		newJitterProvider(),
		newFakeProvider(),
	}
}
