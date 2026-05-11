// SPDX-License-Identifier: Apache-2.0

package errors

import (
	"sync"
	"testing"
	"time"
)

func TestNewQueryIDProducesValidULID(t *testing.T) {
	t.Parallel()
	id := NewQueryID()
	if len(id) != 26 {
		t.Fatalf("ULID length = %d, want 26 (got %q)", len(id), id)
	}
	if _, err := ParseQueryID(id); err != nil {
		t.Fatalf("ParseQueryID returned error on fresh id: %v", err)
	}
}

func TestNewQueryIDTimestampIsRecent(t *testing.T) {
	t.Parallel()
	before := time.Now()
	id := NewQueryID()
	after := time.Now()

	ts, err := ParseQueryID(id)
	if err != nil {
		t.Fatalf("ParseQueryID: %v", err)
	}
	// ULID milliseconds are truncated; allow a millisecond margin on both sides.
	if ts.Before(before.Add(-time.Millisecond)) || ts.After(after.Add(time.Millisecond)) {
		t.Errorf("timestamp %v not in [%v, %v]", ts, before, after)
	}
}

func TestParseQueryIDRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	cases := []string{"", "not-a-ulid", "0000000000000000000000000", "ZZZZZZZZZZZZZZZZZZZZZZZZZZ"}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseQueryID(c); err == nil {
				t.Errorf("ParseQueryID(%q) returned nil error", c)
			}
		})
	}
}

func TestNewQueryIDConcurrent(t *testing.T) {
	t.Parallel()
	const goroutines = 32
	const perG = 200

	seen := sync.Map{}
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range perG {
				id := NewQueryID()
				if _, loaded := seen.LoadOrStore(id, struct{}{}); loaded {
					t.Errorf("duplicate id generated: %q", id)
					return
				}
			}
		}()
	}
	wg.Wait()
}
