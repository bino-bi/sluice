// SPDX-License-Identifier: AGPL-3.0-or-later

package identity

import (
	"context"
	"errors"
	"net/http"
)

// Identifier turns an inbound HTTP request into a UserCtx. Implementations
// are stateless with respect to the request and must be safe for
// concurrent use.
type Identifier interface {
	// Identify inspects r and returns a UserCtx. When the request carries
	// no credential an implementation understands, it must return
	// ErrNoCredential so composite identifiers can try the next method.
	// Any other non-nil error means "this credential was present but
	// invalid" — transports map it to 401.
	Identify(ctx context.Context, r *http.Request) (*UserCtx, error)

	// Name identifies the method for logs and metrics ("jwt", "api_key",
	// "admin_token").
	Name() string
}

// Sentinel errors returned by Identifier implementations.
var (
	// ErrNoCredential indicates the request does not carry a credential
	// this identifier understands. Composite uses it to fall through.
	ErrNoCredential = errors.New("identity: no credential present")

	// ErrInvalidCredential indicates a credential was present but failed
	// verification. Transports map this to HTTP 401.
	ErrInvalidCredential = errors.New("identity: invalid credential")

	// ErrExpiredCredential is a specialisation of ErrInvalidCredential
	// that carries enough context for the audit record.
	ErrExpiredCredential = errors.New("identity: expired credential")
)

// Composite runs each child Identifier in turn. The first non-(nil,
// ErrNoCredential) result is returned. When every child reports
// ErrNoCredential, Composite returns ErrNoCredential so the middleware
// can decide whether to reject the request (default-deny) or let an
// anonymous path through.
type Composite struct {
	children []Identifier
}

// NewComposite builds a Composite from ids. Nil entries are dropped.
func NewComposite(ids ...Identifier) *Composite {
	c := &Composite{children: make([]Identifier, 0, len(ids))}
	for _, id := range ids {
		if id != nil {
			c.children = append(c.children, id)
		}
	}
	return c
}

// Name returns "composite".
func (*Composite) Name() string { return "composite" }

// Identify walks the child identifiers in registration order.
func (c *Composite) Identify(ctx context.Context, r *http.Request) (*UserCtx, error) {
	if c == nil || len(c.children) == 0 {
		return nil, ErrNoCredential
	}
	var firstInvalid error
	for _, child := range c.children {
		uc, err := child.Identify(ctx, r)
		if err == nil {
			return uc, nil
		}
		if errors.Is(err, ErrNoCredential) {
			continue
		}
		// Remember the first "credential present but invalid" error so
		// the caller sees a meaningful 401 reason rather than a generic
		// "no credential" when a bad bearer is later supplanted by a
		// missing API key.
		if firstInvalid == nil {
			firstInvalid = err
		}
	}
	if firstInvalid != nil {
		return nil, firstInvalid
	}
	return nil, ErrNoCredential
}

// Methods returns the names of the registered child identifiers in order.
// Useful for /v1/version and admin diagnostics.
func (c *Composite) Methods() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.children))
	for _, child := range c.children {
		out = append(out, child.Name())
	}
	return out
}
