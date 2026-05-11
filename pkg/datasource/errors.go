// SPDX-License-Identifier: Apache-2.0

package datasource

import "errors"

// Sentinel errors returned by drivers. Wrap with fmt.Errorf for context
// while preserving errors.Is matching.
var (
	// ErrUnknownType is returned by Lookup when the requested type is not
	// registered.
	ErrUnknownType = errors.New("datasource: unknown type")

	// ErrAttach wraps failures during ATTACH.
	ErrAttach = errors.New("datasource: attach failed")

	// ErrHealthCheck wraps health-probe failures.
	ErrHealthCheck = errors.New("datasource: health check failed")

	// ErrSchemaPull wraps schema introspection failures.
	ErrSchemaPull = errors.New("datasource: schema introspection failed")

	// ErrSecretResolve wraps secret resolution failures.
	ErrSecretResolve = errors.New("datasource: secret resolution failed")

	// ErrClosed is returned by methods called on a closed DataSource.
	ErrClosed = errors.New("datasource: closed")

	// ErrReloadNotSupported is returned by drivers that do not implement
	// hot reload and require a full reattach.
	ErrReloadNotSupported = errors.New("datasource: reload not supported")
)
