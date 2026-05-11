// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegistry_PublishAndCurrent(t *testing.T) {
	r := NewRegistry()
	if r.Current() != nil {
		t.Fatal("fresh registry should have nil current")
	}

	s1 := &Snapshot{LoadedAt: time.Now()}
	r.Publish(s1)

	if got := r.Current(); got != s1 {
		t.Fatalf("Current() = %p, want %p", got, s1)
	}
	if s1.Version != 1 {
		t.Errorf("Version = %d, want 1", s1.Version)
	}

	s2 := &Snapshot{LoadedAt: time.Now()}
	r.Publish(s2)
	if s2.Version != 2 {
		t.Errorf("Version = %d, want 2", s2.Version)
	}
}

func TestRegistry_SubscribersReceiveOldAndNew(t *testing.T) {
	r := NewRegistry()

	var mu sync.Mutex
	var calls []struct{ old, current *Snapshot }

	unsub := r.Subscribe(func(old, current *Snapshot) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, struct{ old, current *Snapshot }{old, current})
	})
	defer unsub()

	s1 := &Snapshot{}
	s2 := &Snapshot{}
	r.Publish(s1)
	r.Publish(s2)

	mu.Lock()
	defer mu.Unlock()

	if len(calls) != 2 {
		t.Fatalf("got %d calls, want 2", len(calls))
	}
	if calls[0].old != nil || calls[0].current != s1 {
		t.Errorf("first call: old=%p current=%p", calls[0].old, calls[0].current)
	}
	if calls[1].old != s1 || calls[1].current != s2 {
		t.Errorf("second call: old=%p current=%p", calls[1].old, calls[1].current)
	}
}

func TestRegistry_UnsubscribeStopsDelivery(t *testing.T) {
	r := NewRegistry()

	var count atomic.Int64
	unsub := r.Subscribe(func(_, _ *Snapshot) { count.Add(1) })
	r.Publish(&Snapshot{})
	unsub()
	r.Publish(&Snapshot{})

	if count.Load() != 1 {
		t.Fatalf("count = %d, want 1", count.Load())
	}
}

func TestRegistry_ConcurrentReaders(t *testing.T) {
	r := NewRegistry()
	r.Publish(&Snapshot{})

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if r.Current() == nil {
						t.Error("Current returned nil during concurrent read")
						return
					}
				}
			}
		}()
	}

	for range 50 {
		r.Publish(&Snapshot{})
	}
	close(stop)
	wg.Wait()
}
