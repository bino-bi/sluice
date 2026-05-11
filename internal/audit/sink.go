// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import "context"

// Sink persists pre-hashed audit records. Implementations must be safe for
// concurrent use — the dispatcher serialises writes within a single sink
// but may drive multiple sinks from parallel goroutines.
type Sink interface {
	// Name returns a stable identifier used in metric labels.
	Name() string

	// Record writes r. Implementations may buffer internally; Flush
	// forces durable write.
	Record(ctx context.Context, r *Record) error

	// Flush forces any pending data to persistent storage. Called on a
	// cadence by the dispatcher and on graceful shutdown.
	Flush(ctx context.Context) error

	// Close flushes and releases resources. After Close, further Record
	// calls must return ErrClosed.
	Close(ctx context.Context) error
}
