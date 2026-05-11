// SPDX-License-Identifier: AGPL-3.0-or-later

package datasource

import (
	"context"
	"log/slog"

	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

// HealthProbe exposes a catalog's HealthCheck to external callers
// (admin readiness, tests). Production callers should prefer the ticker
// that runs inside New.
type HealthProbe interface {
	Probe(ctx context.Context, catalog string, conn ConnProvider) error
}

// ConnProvider matches sql.DB.Conn and is the minimum needed to run a
// HealthCheck. Declared locally to avoid pulling in database/sql in
// every call site.
type ConnProvider interface {
	Conn(ctx context.Context) (interface {
		Close() error
	}, error)
}

// healthLoop ticks through every catalog and records Status updates.
// HealthCheck requires a *sql.Conn from the executor pool, which only
// exists once the composition root wires them together. Until the
// executor exists this loop runs but skips the actual probe — it still
// clears out-of-date error states after a reload.
func (r *Registry) healthLoop(ctx context.Context) {
	defer close(r.stopped)

	for {
		select {
		case <-r.stop:
			return
		case <-r.healthTicker.C:
			r.runHealthSweep(ctx)
		}
	}
}

// runHealthSweep walks the registry and fires one HealthCheck per
// catalog. Without a pool the sweep is a no-op; once executor.Pool is
// wired in Slice 2 this function learns to borrow a connection.
func (r *Registry) runHealthSweep(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	catalogs := r.Catalogs()
	for _, name := range catalogs {
		r.recordHealthPlaceholder(name)
	}
}

// recordHealthPlaceholder keeps the Status present in the admin view
// even when no probe has actually run. The executor will replace this
// with a real probe in Slice 2.
func (r *Registry) recordHealthPlaceholder(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.statuses[name]
	if !ok {
		return
	}
	s.LastCheck = r.opts.Clock()
	s.LastLatency = 0
}

// Probe runs a single HealthCheck against a caller-supplied connection
// provider. Primarily used by unit tests and the admin explicit
// /admin/datasources/:name/check endpoint (read-only MVP).
func (r *Registry) Probe(ctx context.Context, catalog string, pool interface {
	Conn(ctx context.Context) (ConnCloser, error)
}) error {
	ds, err := r.Lookup(catalog)
	if err != nil {
		return err
	}
	cc, err := pool.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = cc.Close() }()

	sqlConn, ok := cc.(SQLConn)
	if !ok {
		return errInvalidConn
	}

	timer := r.opts.Clock()
	probeCtx, cancel := context.WithTimeout(ctx, r.opts.HealthTimeout)
	defer cancel()

	err = ds.HealthCheck(probeCtx, sqlConn.SQLConn(), pkgds.HealthOptions{
		Timeout: r.opts.HealthTimeout,
	})

	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.statuses[catalog]
	if s == nil {
		return nil
	}
	s.LastCheck = r.opts.Clock()
	s.LastLatency = r.opts.Clock().Sub(timer)
	if err != nil {
		s.Healthy = false
		s.LastError = err.Error()
		r.log.WarnContext(ctx, "datasource: health probe failed",
			slog.String("catalog", catalog),
			slog.String("error", err.Error()),
		)
		return err
	}
	s.Healthy = true
	s.LastError = ""
	return nil
}
