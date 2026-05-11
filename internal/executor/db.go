// SPDX-License-Identifier: AGPL-3.0-or-later

package executor

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	duckdb "github.com/marcboeker/go-duckdb/v2"
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

	connector, err := duckdb.NewConnector(cfg.Path, nil)
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

// Close drains the pool and releases the DuckDB instance. Safe to call
// multiple times.
func (e *Executor) Close() error {
	if e == nil || e.db == nil {
		return nil
	}
	return e.db.Close()
}

// ensureInit runs the attach + hardening sequence on conn if the
// connection hasn't already been initialised. The order matters:
// data-source drivers need to INSTALL/LOAD extensions (which requires
// external access) and ATTACH their catalogs before we lock the
// connection down. We detect fresh connections by probing
// lock_configuration — DuckDB starts with it false and, post-hardening,
// we set it true; once true, the setting can't be changed for the
// connection's lifetime. That means "lock_configuration = true" is a
// reliable, connection-local fingerprint for "already hardened".
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
