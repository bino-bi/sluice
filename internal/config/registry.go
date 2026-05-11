// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"sync"
	"sync/atomic"
)

// Registry holds the currently-active Snapshot and broadcasts successor
// snapshots to Subscribers. Readers never block; writers swap atomically.
// The MVP slice exposes Publish only; the hot-reload Watcher lands later.
type Registry struct {
	current atomic.Pointer[Snapshot]
	mu      sync.Mutex // guards subs + version sequencing during Publish
	subs    []subscription
	nextID  uint64
	version int64
}

// Subscriber is invoked synchronously for every successful Publish. old is
// nil for the first publish. Subscribers run in registration order, holding
// no lock across user code, so they must not block indefinitely.
type Subscriber func(old, current *Snapshot)

// NewRegistry returns an empty Registry with no active snapshot.
func NewRegistry() *Registry { return &Registry{} }

// Current returns the active snapshot, or nil if nothing has been published.
func (r *Registry) Current() *Snapshot { return r.current.Load() }

// Subscribe registers fn for future publishes and returns an unsubscribe
// closure. Subscribers added after a Publish do not receive earlier snapshots.
func (r *Registry) Subscribe(fn Subscriber) (unsubscribe func()) {
	r.mu.Lock()
	id := r.nextID
	r.nextID++
	r.subs = append(r.subs, subscription{id: id, fn: fn})
	r.mu.Unlock()

	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		for i, s := range r.subs {
			if s.id == id {
				r.subs = append(r.subs[:i], r.subs[i+1:]...)
				return
			}
		}
	}
}

// Publish atomically swaps next into place and notifies subscribers. The
// previous snapshot is passed to each subscriber as old (nil on first call).
// Publish assigns an increasing Version so downstream components can detect
// out-of-order or replayed snapshots.
func (r *Registry) Publish(next *Snapshot) {
	r.mu.Lock()
	r.version++
	next.Version = r.version
	subsCopy := append([]subscription(nil), r.subs...)
	r.mu.Unlock()

	old := r.current.Swap(next)

	for _, s := range subsCopy {
		s.fn(old, next)
	}
}

type subscription struct {
	id uint64
	fn Subscriber
}
