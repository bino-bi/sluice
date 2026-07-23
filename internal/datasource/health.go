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

// ConnProvider hands out pooled connections for health probes; the
// minimum needed to run a HealthCheck. Satisfied by NewSQLPool
// (production) and by test fakes. Declared locally to avoid pulling in
// database/sql in every call site.
type ConnProvider interface {
	Conn(ctx context.Context) (ConnCloser, error)
}

// healthLoop ticks through every catalog and records Status updates.
// HealthCheck requires a *sql.Conn from the executor pool, which only
// exists once the composition root calls SetPool. Until then the loop
// runs but every sweep is a no-op and statuses keep their fail-closed
// Healthy=false.
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

// SetPool wires the executor-backed connection pool the health sweep
// borrows probe connections from. Called by the composition root once
// the executor exists (the Registry is constructed first because the
// executor needs its AttachHook). Triggers one immediate sweep so
// readiness reflects real state shortly after boot rather than after
// the first full HealthInterval.
func (r *Registry) SetPool(ctx context.Context, pool ConnProvider) {
	r.mu.Lock()
	r.pool = pool
	r.mu.Unlock()
	select {
	case <-r.stop:
		return
	default:
	}
	go r.runHealthSweep(ctx)
}

// runHealthSweep fires one Probe per catalog against the wired pool.
// Nil pool (SetPool not called yet, or a CLI-only composition): skip —
// statuses keep their fail-closed Healthy=false until a probe succeeds.
func (r *Registry) runHealthSweep(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	r.mu.RLock()
	pool := r.pool
	r.mu.RUnlock()
	if pool == nil {
		return
	}
	for _, name := range r.Catalogs() {
		// Probe records the status transition and logs failures.
		_ = r.Probe(ctx, name, pool)
	}
}

// Probe runs a single HealthCheck against a caller-supplied connection
// provider and records the Status transition. Used by the periodic
// sweep, unit tests, and the admin explicit check path.
func (r *Registry) Probe(ctx context.Context, catalog string, pool ConnProvider) error {
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
