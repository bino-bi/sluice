// SPDX-License-Identifier: AGPL-3.0-or-later

package schema

import (
	"context"
	"fmt"
	"sort"
)

// NewStatic returns a Cache backed by a fixed set of entries. It never
// introspects a data source: Get misses return ErrUnknownTable, the
// invalidation methods are no-ops, and Refresh always succeeds. Intended
// for fixtures and tests (policy test suites, golden rewrite fixtures);
// production uses New.
func NewStatic(entries []*Entry) Cache {
	s := &staticCache{entries: make(map[Key]*Entry, len(entries))}
	for _, e := range entries {
		if e == nil {
			continue
		}
		s.entries[e.Key] = e
	}
	return s
}

type staticCache struct {
	entries map[Key]*Entry
}

func (s *staticCache) Get(_ context.Context, key Key) (*Entry, error) {
	if e, ok := s.entries[key]; ok {
		return e, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrUnknownTable, key)
}

func (s *staticCache) All() []*Entry {
	out := make([]*Entry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key.String() < out[j].Key.String() })
	return out
}

func (s *staticCache) Invalidate(Key)                          {}
func (s *staticCache) InvalidateCatalog(string)                {}
func (s *staticCache) InvalidateAll()                          {}
func (s *staticCache) Refresh(context.Context, []string) error { return nil }
