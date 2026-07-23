// SPDX-License-Identifier: AGPL-3.0-or-later

package datasource

import (
	"context"
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

// NewSQLPool adapts a *sql.DB (the executor's pool) to ConnProvider so
// the health sweep can borrow probe connections. The returned conns
// satisfy SQLConn, which Probe requires.
func NewSQLPool(db *sql.DB) ConnProvider { return sqlPool{db: db} }

type sqlPool struct{ db *sql.DB }

func (p sqlPool) Conn(ctx context.Context) (ConnCloser, error) {
	c, err := p.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	return sqlPoolConn{c: c}, nil
}

type sqlPoolConn struct{ c *sql.Conn }

func (s sqlPoolConn) Close() error       { return s.c.Close() }
func (s sqlPoolConn) SQLConn() *sql.Conn { return s.c }
