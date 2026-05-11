// SPDX-License-Identifier: Apache-2.0

package errors

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// ULID generation needs a monotonic entropy source. ulid.Monotonic is not
// safe for concurrent use, so we serialize with a sync.Mutex. A sync.Pool
// would also work, but the mutex is simpler and the lock hold is a few ns.
var (
	entropyMu sync.Mutex
	entropy   = ulid.Monotonic(rand.Reader, 0)
)

// NewQueryID returns a fresh ULID-encoded query ID. ULIDs are
// lexicographically sortable and embed a 48-bit millisecond timestamp that
// simplifies audit-log seeking.
func NewQueryID() string {
	entropyMu.Lock()
	defer entropyMu.Unlock()
	return ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String()
}

// ParseQueryID validates the format and returns the embedded timestamp.
// It returns an error on invalid input; callers must not assume parse
// success implies the ID was ever issued by this process.
func ParseQueryID(s string) (time.Time, error) {
	id, err := ulid.Parse(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("errors: invalid query id: %w", err)
	}
	return ulid.Time(id.Time()), nil
}
