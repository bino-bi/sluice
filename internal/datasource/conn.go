// SPDX-License-Identifier: AGPL-3.0-or-later

package datasource

import (
	"database/sql"
	"errors"
)

// ConnCloser is the minimum interface this package needs from a pool's
// returned connection. Declared locally so unit tests can supply fakes
// without depending on database/sql.
type ConnCloser interface {
	Close() error
}

// SQLConn exposes the underlying *sql.Conn from a ConnCloser when one
// is available. The production executor implementation always returns
// a real *sql.Conn; tests that don't need a real connection return nil.
type SQLConn interface {
	ConnCloser
	SQLConn() *sql.Conn
}

// errInvalidConn is returned when a Probe is given a pool whose Conn()
// implementation does not satisfy SQLConn.
var errInvalidConn = errors.New("datasource: pool did not return a *sql.Conn")
