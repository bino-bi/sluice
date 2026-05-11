// SPDX-License-Identifier: Apache-2.0

package datasource

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Factory constructs a DataSource from a Spec.
type Factory func(ctx context.Context, spec Spec) (DataSource, error)

// registry is the package-level factory table.
var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register installs a Factory for a data-source type. Registering the same
// type twice panics: duplicate registration indicates a configuration bug
// that should surface at program-init time, not produce silent overrides.
func Register(typ string, f Factory) {
	if typ == "" {
		panic("datasource: Register: type is empty")
	}
	if f == nil {
		panic(fmt.Sprintf("datasource: Register(%q): factory is nil", typ))
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[typ]; exists {
		panic(fmt.Sprintf("datasource: Register(%q): type already registered", typ))
	}
	registry[typ] = f
}

// Lookup returns the Factory for the given type.
func Lookup(typ string) (Factory, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	f, ok := registry[typ]
	return f, ok
}

// Types returns the sorted list of registered data-source types.
func Types() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// resetRegistryForTest is an internal helper for tests that need a clean
// registry state. It is NOT part of the public API — tests in the same
// package reach through this identifier directly.
func resetRegistryForTest() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = map[string]Factory{}
}
