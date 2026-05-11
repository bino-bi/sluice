// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import "errors"

// ErrQueueFull is returned by Dispatcher.Enqueue when the bounded queue is
// saturated and the enqueue deadline expired. Transports treat it as an
// internal error — audit MUST be emitted for every policy decision, so a
// persistently full queue is a configuration problem, not a client one.
var ErrQueueFull = errors.New("audit: queue full")

// ErrClosed is returned by any Dispatcher method called after Close.
var ErrClosed = errors.New("audit: dispatcher closed")

// ErrChainBroken is the sentinel returned by Verify when a record's hash
// does not match the recomputed value, or when a file's first record does
// not chain to the previous file's last hash.
var ErrChainBroken = errors.New("audit: chain broken")
