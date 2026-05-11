// SPDX-License-Identifier: Apache-2.0

package mask

import "errors"

// Sentinel errors returned by provider methods. Wrap with fmt.Errorf("...:
// %w", err) to add context while preserving errors.Is matching.
var (
	// ErrSQLOnly is returned by providers that implement only MaskSQL.
	ErrSQLOnly = errors.New("mask: provider is SQL-only; MaskArrow not supported")

	// ErrArrowOnly is returned by providers that implement only MaskArrow.
	ErrArrowOnly = errors.New("mask: provider is Arrow-only; MaskSQL not supported")

	// ErrInvalidArgs is returned by ValidateArgs when the args payload is
	// malformed.
	ErrInvalidArgs = errors.New("mask: invalid args")

	// ErrResolveSecret is returned when a secret reference (SaltRef, KeyRef)
	// cannot be resolved.
	ErrResolveSecret = errors.New("mask: failed to resolve secret reference")

	// ErrDuplicateType is returned by Registry.Register when a provider is
	// registered twice under the same Type.
	ErrDuplicateType = errors.New("mask: provider type already registered")

	// ErrUnknownType is returned by Registry.Lookup (through the ok=false
	// boolean — callers that prefer an error use this sentinel).
	ErrUnknownType = errors.New("mask: unknown provider type")
)
