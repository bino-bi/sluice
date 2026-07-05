// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import "errors"

// Sentinel errors returned by the broker.
var (
	// ErrBrokerFull is returned by Require when the pending-request cap is
	// reached. Fail-closed: a new query cannot park.
	ErrBrokerFull = errors.New("approval: pending request limit reached")
	// ErrNotFound is returned when an approval id is unknown.
	ErrNotFound = errors.New("approval: request not found")
	// ErrTokenMismatch is returned when a capability token does not match.
	ErrTokenMismatch = errors.New("approval: token mismatch")
	// ErrAlreadyDecided is returned when accept/reject is called on a
	// request that was already decided with the opposite verb.
	ErrAlreadyDecided = errors.New("approval: request already decided")
)
