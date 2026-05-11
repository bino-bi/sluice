// SPDX-License-Identifier: Apache-2.0

package errors

import (
	stderrors "errors"
	"fmt"
	"maps"
)

// APIError is the JSON shape returned to clients on 4xx/5xx responses and
// embedded in MCP tool-error responses. Unknown (non-APIError) errors are
// normalized through FromError before reaching the wire.
type APIError struct {
	Code    Code           `json:"code"`
	Message string         `json:"message"`
	QueryID string         `json:"query_id,omitempty"`
	Policy  string         `json:"policy,omitempty"`
	Details map[string]any `json:"details,omitempty"`

	// status caches the HTTP status so Status() is O(1) and constructors
	// can override the default. Unexported to force construction via New*.
	status int
	// cause is the wrapped error for errors.Is / errors.Unwrap chains.
	cause error
}

// Error implements the error interface. Format: "<CODE>: <message>". The
// wrapped cause, if any, is not included — callers use errors.Unwrap to
// traverse the chain.
func (e *APIError) Error() string {
	return string(e.Code) + ": " + e.Message
}

// Status returns the HTTP status associated with the error.
func (e *APIError) Status() int { return e.status }

// Unwrap returns the wrapped cause, if any, to support errors.Is.
func (e *APIError) Unwrap() error { return e.cause }

// Is reports whether the target matches this error by Code. Enables
// `errors.Is(err, New(CodeACLDenied))` without requiring pointer identity.
func (e *APIError) Is(target error) bool {
	var t *APIError
	if stderrors.As(target, &t) {
		return t.Code == e.Code
	}
	return false
}

// New returns an APIError carrying the canonical message and HTTP status
// for the code. Use WithMessage/WithQueryID/WithPolicy/WithDetail to
// enrich it; the builders are non-mutating (they return a new *APIError).
func New(code Code) *APIError {
	return &APIError{
		Code:    code,
		Message: Message(code),
		status:  Status(code),
	}
}

// Newf returns an APIError with a formatted, client-safe message. Callers
// must not embed secrets or internal identifiers in the format arguments.
func Newf(code Code, format string, args ...any) *APIError {
	e := New(code)
	e.Message = fmt.Sprintf(format, args...)
	return e
}

// Wrap annotates a cause with an APIError envelope. The cause is retained
// for errors.Is but is not rendered in the public message.
func Wrap(code Code, cause error) *APIError {
	e := New(code)
	e.cause = cause
	return e
}

// WithMessage returns a copy of e with the message replaced.
func (e *APIError) WithMessage(msg string) *APIError {
	cp := e.clone()
	cp.Message = msg
	return cp
}

// WithQueryID returns a copy of e with the query_id set. Transports set
// this on every error leaving a handler.
func (e *APIError) WithQueryID(id string) *APIError {
	cp := e.clone()
	cp.QueryID = id
	return cp
}

// WithPolicy returns a copy of e with the policy name set. Populated for
// ACL_DENIED / ACL_REJECTED to tell the client which policy fired.
func (e *APIError) WithPolicy(name string) *APIError {
	cp := e.clone()
	cp.Policy = name
	return cp
}

// WithDetail returns a copy of e with key=value added to Details. Values
// must be JSON-encodable and must not contain sensitive information.
func (e *APIError) WithDetail(key string, value any) *APIError {
	cp := e.clone()
	if cp.Details == nil {
		cp.Details = map[string]any{}
	} else {
		cp.Details = maps.Clone(cp.Details)
	}
	cp.Details[key] = value
	return cp
}

// FromError walks the error chain looking for an *APIError. When found,
// the first match wins. When no APIError is present, FromError returns
// New(CodeInternal) with no details — a public-safe default that refuses
// to leak internal errors by accident.
func FromError(err error) *APIError {
	if err == nil {
		return nil
	}
	var e *APIError
	if stderrors.As(err, &e) {
		return e
	}
	return New(CodeInternal)
}

// clone returns a shallow copy of e. Details is copied lazily in
// WithDetail; other fields are value-typed so a shallow copy is safe.
func (e *APIError) clone() *APIError {
	cp := *e
	return &cp
}
