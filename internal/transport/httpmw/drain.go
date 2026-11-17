// SPDX-License-Identifier: AGPL-3.0-or-later

package httpmw

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// handlerGrace bounds the wait for handlers after their contexts were
// cancelled. A handler that ignores its context must not block the exit.
var handlerGrace = 5 * time.Second

// Drain stops an http.Server in two steps. First a graceful Shutdown. If
// requests still run when it times out, Drain cancels their contexts,
// closes the server and waits for the handlers to return. So the caller
// can close what the handlers use (audit, budget, DuckDB) after Shutdown.
//
// Set BaseContext on the server and wrap its handler with Track.
type Drain struct {
	ctx    context.Context
	cancel context.CancelFunc
	active atomic.Int64
}

// NewDrain returns a Drain with a live base context.
func NewDrain() *Drain {
	ctx, cancel := context.WithCancel(context.Background())
	return &Drain{ctx: ctx, cancel: cancel}
}

// BaseContext is the http.Server.BaseContext hook. Every request context
// derives from it.
func (d *Drain) BaseContext(net.Listener) context.Context { return d.ctx }

// Track counts the handlers that run.
func (d *Drain) Track(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.active.Add(1)
		defer d.active.Add(-1)
		next.ServeHTTP(w, r)
	})
}

// Shutdown drains srv for up to timeout, then stops the requests that are
// left. It returns once no handler runs, or after the grace period for
// handlers that ignore their context. A drain timeout is logged, not
// returned: it is a normal way to stop.
func (d *Drain) Shutdown(parent context.Context, srv *http.Server, timeout time.Duration, log *slog.Logger, name string) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	err := srv.Shutdown(ctx)
	if err == nil && d.active.Load() == 0 {
		d.cancel()
		return
	}
	if err != nil {
		log.Warn(name+": drain timed out, cancelling the requests that still run",
			"timeout", timeout.String(), "active", d.active.Load())
	}
	d.cancel()
	_ = srv.Close()
	deadline := time.Now().Add(handlerGrace)
	for d.active.Load() > 0 {
		if time.Now().After(deadline) {
			log.Error(name+": handlers did not return after cancel",
				"active", d.active.Load())
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
