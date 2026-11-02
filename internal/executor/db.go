// SPDX-License-Identifier: AGPL-3.0-or-later

package executor

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"
)

// Config tunes the embedded DuckDB and the wrapping *sql.DB pool.
type Config struct {
	// Path is the DuckDB database path. Empty means an in-memory,
	// ephemeral database — the MVP default for stateless gateways and
	// unit tests.
	Path string

	// Harden controls the connection-init SET statements. The security
	// floor is always applied; these override defaults for the tunables.
	Harden HardenConfig

	// AttachHook is invoked on every fresh connection after hardening.
	// The datasource.Registry supplies this; nil means "no catalogs
	// attached" (useful for unit tests).
	AttachHook func(ctx context.Context, conn *sql.Conn) error

	// Pool knobs — pass zero to take the defaults.
	MaxOpen     int
	MaxIdle     int
	ConnMaxIdle time.Duration

	// DefaultStatementTimeout caps a single Execute when the Request
	// does not specify its own. Zero disables (context.Context timeouts
	// still apply).
	DefaultStatementTimeout time.Duration

	Logger *slog.Logger
}

// Defaults for the pool + timeouts. Chosen to match the `internal-executor`
// plan file §4.1.
const (
	DefaultMaxOpen                 = 4
	DefaultMaxIdle                 = 2
	DefaultConnMaxIdle             = 5 * time.Minute
	DefaultDefaultStatementTimeout = 30 * time.Second
)

// Executor wraps a *sql.DB with hardened per-connection init and
// exposes Execute for the queryservice.
type Executor struct {
	cfg Config
	db  *sql.DB
	log *slog.Logger

	// attachMu orders catalog swaps against query starts. ATTACH and
	// DETACH change the whole instance, so Exclusive holds the write
	// side while it swaps; Execute holds the read side from ensureInit
	// until QueryContext returns. A result that streams after that keeps
	// reading even when its catalog is detached.
	attachMu sync.RWMutex
}

// New opens the embedded DuckDB instance and returns an Executor. A
// warmup Ping is run so any hardening failure surfaces at construction
// rather than on the first user request.
func New(ctx context.Context, cfg Config) (*Executor, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.MaxOpen <= 0 {
		cfg.MaxOpen = DefaultMaxOpen
	}
	if cfg.MaxIdle <= 0 {
		cfg.MaxIdle = DefaultMaxIdle
	}
	if cfg.ConnMaxIdle <= 0 {
		cfg.ConnMaxIdle = DefaultConnMaxIdle
	}
	if cfg.DefaultStatementTimeout <= 0 {
		cfg.DefaultStatementTimeout = DefaultDefaultStatementTimeout
	}

	// allow_persistent_secrets must be set at open time, not in the
	// per-connection hardening pass: httpfs initialises the secret
	// manager the moment a driver binds a remote path during attach, and
	// DuckDB refuses to change secret-manager settings after first use.
	// Drivers still create TEMPORARY secrets (the CREATE SECRET default).
	dsn := cfg.Path + "?allow_persistent_secrets=false"
	if strings.Contains(cfg.Path, "?") {
		dsn = cfg.Path + "&allow_persistent_secrets=false"
	}
	connector, err := duckdb.NewConnector(dsn, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrOpen, err)
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(cfg.MaxOpen)
	db.SetMaxIdleConns(cfg.MaxIdle)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdle)

	e := &Executor{cfg: cfg, db: db, log: cfg.Logger}

	// Validate the connection immediately so hardening misconfiguration
	// surfaces during boot rather than at request time.
	if err := e.Ping(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return e, nil
}

// DB returns the underlying *sql.DB. Exposed for transports that need a
// handle (e.g., otelsql tracing wrappers).
func (e *Executor) DB() *sql.DB { return e.db }

// Ping acquires a fresh connection, runs init if needed, and executes
// SELECT 1. Used by the readiness probe and at construction.
func (e *Executor) Ping(ctx context.Context) error {
	conn, err := e.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrOpen, err)
	}
	defer func() { _ = conn.Close() }()
	e.attachMu.RLock()
	defer e.attachMu.RUnlock()
	if err := e.ensureInit(ctx, conn); err != nil {
		return err
	}
	var one int
	if err := conn.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("%w: %w", ErrExecute, err)
	}
	if one != 1 {
		return fmt.Errorf("%w: SELECT 1 returned %d", ErrExecute, one)
	}
	return nil
}

// CurrentDatabase returns the name of DuckDB's default database, which
// holds a table written without a catalog.
func (e *Executor) CurrentDatabase(ctx context.Context) (string, error) {
	conn, err := e.db.Conn(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrOpen, err)
	}
	defer func() { _ = conn.Close() }()
	e.attachMu.RLock()
	defer e.attachMu.RUnlock()
	if err := e.ensureInit(ctx, conn); err != nil {
		return "", err
	}
	var name string
	if err := conn.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil {
		return "", fmt.Errorf("%w: %w", ErrExecute, err)
	}
	return name, nil
}

// exclusivePoll is how often Exclusive tries the write lock.
const exclusivePoll = 10 * time.Millisecond

// Exclusive runs fn on a hardened connection while no query starts. It
// is for changes to the whole instance, such as a DETACH and ATTACH of a
// catalog. It does not wait for open results to close. The connection
// is taken before the lock, so a full pool does not block new queries
// while Exclusive waits. New queries also keep starting while Exclusive
// waits for running ones, so under steady load it may wait until ctx ends.
func (e *Executor) Exclusive(ctx context.Context, fn func(ctx context.Context, conn *sql.Conn) error) error {
	conn, err := e.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrConnUnavailable, err)
	}
	defer func() { _ = conn.Close() }()
	// A blocked Lock makes every new RLock wait behind it, and QueryContext
	// holds RLock until a blocking operator (aggregate, sort) is done. So
	// one long query would stall all query starts. TryLock does not.
	for !e.attachMu.TryLock() {
		select {
		case <-ctx.Done():
			return wrapRunErr(ctx.Err())
		case <-time.After(exclusivePoll):
		}
	}
	defer e.attachMu.Unlock()
	if err := e.ensureInit(ctx, conn); err != nil {
		return err
	}
	return fn(ctx, conn)
}

// Close drains the pool and releases the DuckDB instance. Safe to call
// multiple times.
func (e *Executor) Close() error {
	if e == nil || e.db == nil {
		return nil
	}
	return e.db.Close()
}

// ensureInit runs the attach + hardening sequence on conn if the
// instance hasn't already been initialised. The order matters:
// data-source drivers need to INSTALL/LOAD extensions (which requires
// external access) and ATTACH their catalogs before we lock the
// configuration down. We detect a fresh instance by probing
// lock_configuration. DuckDB starts with it false and, post-hardening,
// we set it true; once true, the setting can't be changed. Settings and
// ATTACH belong to the whole DuckDB instance, not to one connection, so
// "lock_configuration = true" means the instance is already hardened
// and every catalog is attached. In practice the first connection does
// the work and later connections skip it.
func (e *Executor) ensureInit(ctx context.Context, conn *sql.Conn) error {
	var locked bool
	row := conn.QueryRowContext(ctx, "SELECT value::BOOLEAN FROM duckdb_settings() WHERE name='lock_configuration'")
	if err := row.Scan(&locked); err != nil {
		return fmt.Errorf("%w: probe lock_configuration: %w", ErrExecute, err)
	}
	if locked {
		return nil
	}
	if e.cfg.AttachHook != nil {
		if err := e.cfg.AttachHook(ctx, conn); err != nil {
			return fmt.Errorf("%w: %w", ErrAttach, err)
		}
	}
	return applyHardening(ctx, conn, e.cfg.Harden)
}
