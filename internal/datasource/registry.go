// SPDX-License-Identifier: AGPL-3.0-or-later

package datasource

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

// Status reports the current state of a single attached catalog. It is
// updated by the health-check loop and read by the admin API + readiness
// probe.
type Status struct {
	Name        string
	Type        string
	Healthy     bool
	LastCheck   time.Time
	LastError   string
	LastLatency time.Duration
	// SchemaPulledAt is the most-recent time the schema cache finished
	// loading this catalog. Zero until the first successful Refresh.
	SchemaPulledAt time.Time
}

// AttachHook is the callback the executor installs on every new DuckDB
// connection. It iterates every healthy data source in the Registry and
// runs DataSource.Attach against conn.
type AttachHook func(ctx context.Context, conn *sql.Conn) error

// Options configures a Registry.
type Options struct {
	// Snapshot is the current policy/config snapshot. Required at
	// construction; the Registry does not subscribe to updates itself —
	// the composition root wires that when hot-reload lands.
	Snapshot *Snapshot

	// Secrets resolves secret:// URIs referenced by data-source specs.
	// Required when any data source references a secret.
	Secrets pkgds.SecretResolver

	// Logger receives slog output. Nil uses slog.Default.
	Logger *slog.Logger

	// HealthInterval is the tick period for the health-check loop.
	// Zero means DefaultHealthInterval. Set to -1 to disable the loop
	// entirely (tests rely on this).
	HealthInterval time.Duration

	// HealthTimeout caps each individual HealthCheck call. Zero means
	// DefaultHealthTimeout.
	HealthTimeout time.Duration

	// FailFast rejects New with an error if any data source fails to
	// construct. Otherwise failing data sources are skipped and the
	// Registry starts in "degraded" mode.
	FailFast bool

	// Clock returns "now". Nil uses time.Now.
	Clock func() time.Time
}

// Snapshot is the subset of the config snapshot this package reads.
// Declared locally to avoid importing internal/config (which would
// create a layering inversion — config is infrastructure, datasource is
// a core service).
type Snapshot struct {
	DataSources []*apitypes.DataSource
}

// Defaults — tuned for the MVP workload.
const (
	DefaultHealthInterval = 30 * time.Second
	DefaultHealthTimeout  = 5 * time.Second
)

// ErrUnknownCatalog is returned by Lookup when no DataSource with the
// requested name is registered.
var ErrUnknownCatalog = errors.New("datasource: unknown catalog")

// Registry owns every instantiated DataSource.
type Registry struct {
	opts Options
	log  *slog.Logger

	mu       sync.RWMutex
	sources  map[string]pkgds.DataSource
	statuses map[string]*Status
	order    []string // insertion order for deterministic iteration
	// pool hands out probe connections for the health sweep; nil until
	// the composition root calls SetPool (the executor is built after
	// the Registry because it needs the AttachHook).
	pool ConnProvider

	// healthTicker can be nil when HealthInterval <= 0.
	healthTicker *time.Ticker
	stop         chan struct{}
	stopped      chan struct{}
}

// New builds a Registry from the supplied Options. Every data source
// declared in opts.Snapshot is resolved to a Factory via pkg/datasource
// and instantiated. Instantiation errors are aggregated; when
// opts.FailFast is true the first error is returned.
func New(ctx context.Context, opts Options) (*Registry, error) {
	if opts.Snapshot == nil {
		return nil, errors.New("datasource: Options.Snapshot is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.HealthInterval == 0 {
		opts.HealthInterval = DefaultHealthInterval
	}
	if opts.HealthTimeout <= 0 {
		opts.HealthTimeout = DefaultHealthTimeout
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}

	r := &Registry{
		opts:     opts,
		log:      opts.Logger,
		sources:  make(map[string]pkgds.DataSource),
		statuses: make(map[string]*Status),
		stop:     make(chan struct{}),
		stopped:  make(chan struct{}),
	}

	var firstErr error
	for _, spec := range opts.Snapshot.DataSources {
		if err := r.addDataSource(ctx, spec); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if opts.FailFast {
				_ = r.Close()
				return nil, err
			}
			r.log.WarnContext(ctx, "datasource: skipped (degraded mode)",
				slog.String("name", spec.Metadata.Name),
				slog.String("type", string(spec.Spec.Type)),
				slog.String("error", err.Error()),
			)
			continue
		}
	}

	if opts.HealthInterval > 0 {
		r.healthTicker = time.NewTicker(opts.HealthInterval)
		go r.healthLoop(context.Background())
	} else {
		close(r.stopped)
	}

	// Non-fatal bootstrap errors propagate as a nil Registry value when
	// FailFast was set; otherwise they've been logged and the Registry
	// is returned in degraded mode.
	_ = firstErr
	return r, nil
}

// addDataSource instantiates and records a single data source. Caller
// holds no lock; this function takes the write lock internally.
func (r *Registry) addDataSource(ctx context.Context, spec *apitypes.DataSource) error {
	if spec == nil || spec.Metadata.Name == "" {
		return errors.New("datasource: spec missing metadata.name")
	}
	typ := string(spec.Spec.Type)
	factory, ok := pkgds.Lookup(typ)
	if !ok {
		return fmt.Errorf("datasource %q: unknown type %q (registered: %v)", spec.Metadata.Name, typ, pkgds.Types())
	}
	driverSpec := pkgds.Spec{
		Name:    spec.Metadata.Name,
		Type:    typ,
		Raw:     marshalSpec(spec.Spec),
		Secrets: r.opts.Secrets,
		Filters: pkgds.Filters{
			Schemas: spec.Spec.Schemas,
			Tables:  spec.Spec.Tables,
		},
	}

	ds, err := factory(ctx, driverSpec)
	if err != nil {
		return fmt.Errorf("datasource %q: factory: %w", spec.Metadata.Name, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.sources[spec.Metadata.Name]; dup {
		_ = ds.Close()
		return fmt.Errorf("datasource %q: duplicate name", spec.Metadata.Name)
	}
	r.sources[spec.Metadata.Name] = ds
	r.statuses[spec.Metadata.Name] = &Status{
		Name:    spec.Metadata.Name,
		Type:    typ,
		Healthy: false, // first health check populates this.
	}
	r.order = append(r.order, spec.Metadata.Name)
	return nil
}

// marshalSpec flattens apitypes.DataSourceSpec into the free-form Raw
// map drivers consume. We deliberately avoid round-tripping through JSON
// so this stays allocation-light on the composition hot path.
func marshalSpec(s apitypes.DataSourceSpec) map[string]any {
	raw := map[string]any{
		"type":     string(s.Type),
		"readonly": s.Readonly,
	}
	if s.Connection != "" {
		raw["connection"] = s.Connection
	}
	if s.CredentialsRef != "" {
		raw["credentialsRef"] = s.CredentialsRef
	}
	if s.Path != "" {
		raw["path"] = s.Path
	}
	if s.Bucket != "" {
		raw["bucket"] = s.Bucket
	}
	if s.Prefix != "" {
		raw["prefix"] = s.Prefix
	}
	if s.Region != "" {
		raw["region"] = s.Region
	}
	if len(s.AllowedPaths) > 0 {
		raw["allowedPaths"] = append([]string(nil), s.AllowedPaths...)
	}
	if s.Endpoint != "" {
		raw["endpoint"] = s.Endpoint
	}
	if s.Database != "" {
		raw["database"] = s.Database
	}
	if s.TokenRef != "" {
		raw["tokenRef"] = s.TokenRef
	}
	if s.AttachMode != "" {
		raw["attachMode"] = string(s.AttachMode)
	}
	return raw
}

// Lookup returns the DataSource for the given catalog name, or
// ErrUnknownCatalog when unregistered. The schema cache's
// DataSourceResolver interface is satisfied by this method.
func (r *Registry) Lookup(name string) (pkgds.DataSource, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ds, ok := r.sources[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownCatalog, name)
	}
	return ds, nil
}

// Catalogs returns the sorted list of registered catalog names.
func (r *Registry) Catalogs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.sources))
	for name := range r.sources {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Statuses returns a snapshot of every catalog's current status in
// insertion order (so admin UIs stay stable between reloads).
func (r *Registry) Statuses() []Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Status, 0, len(r.order))
	for _, name := range r.order {
		if s, ok := r.statuses[name]; ok {
			out = append(out, *s)
		}
	}
	return out
}

// AttachHook returns a hook suitable for installing on every fresh
// DuckDB connection. It iterates every catalog and calls Attach; the
// first failure aborts subsequent attachments — the executor treats any
// error from AttachHook as a fatal connection error.
func (r *Registry) AttachHook() AttachHook {
	return func(ctx context.Context, conn *sql.Conn) error {
		r.mu.RLock()
		order := append([]string(nil), r.order...)
		sources := make(map[string]pkgds.DataSource, len(r.sources))
		for k, v := range r.sources {
			sources[k] = v
		}
		r.mu.RUnlock()

		for _, name := range order {
			ds := sources[name]
			if ds == nil {
				continue
			}
			opts := pkgds.AttachOptions{
				CatalogName:    ds.Name(),
				SecretResolver: r.opts.Secrets,
			}
			if err := ds.Attach(ctx, conn, opts); err != nil {
				return fmt.Errorf("datasource %q: attach: %w", name, err)
			}
		}
		return nil
	}
}

// Close stops the health loop and closes every data source. Safe to call
// multiple times; subsequent calls are no-ops.
func (r *Registry) Close() error {
	r.mu.Lock()
	select {
	case <-r.stop:
		// already stopped
		r.mu.Unlock()
		return nil
	default:
		close(r.stop)
	}
	sources := r.sources
	r.sources = nil
	r.mu.Unlock()

	if r.healthTicker != nil {
		r.healthTicker.Stop()
		<-r.stopped
	}
	var firstErr error
	for _, ds := range sources {
		if err := ds.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// MarkSchemaPulled records that the schema cache finished an
// introspection for a catalog. Consumed by the admin status view.
func (r *Registry) MarkSchemaPulled(catalog string, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.statuses[catalog]; ok {
		s.SchemaPulledAt = at
	}
}
