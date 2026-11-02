// SPDX-License-Identifier: AGPL-3.0-or-later

package synthetic

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/bino-bi/sluice/internal/datasource/drivers/common"
	"github.com/bino-bi/sluice/internal/synth/build"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

// Type matches apitypes.DSSynthetic.
const Type = string(apitypes.DSSynthetic)

// Build states reported by State.
const (
	StateNotBuilt = "not_built"
	StateReady    = "ready"
	StateStale    = "stale"
)

const memoryDB = ":memory:"

type driver struct {
	name     string
	layout   build.Layout
	specHash string

	mu sync.Mutex
	// path is the attached snapshot, or memoryDB when not built.
	path  string
	state string
}

// Register installs the factory with pkg/datasource. Call once at
// program start; subsequent calls panic (contract on pkg/datasource.Register).
func Register() { pkgds.Register(Type, newDriver) }

func newDriver(_ context.Context, spec pkgds.Spec) (pkgds.DataSource, error) {
	syn, _ := spec.Raw["synthetic"].(*apitypes.SyntheticSpec)
	if syn == nil {
		return nil, errors.New("synthetic: spec.synthetic is required")
	}
	dir, _ := spec.Raw["syntheticDir"].(string)
	if dir == "" {
		return nil, errors.New("synthetic: synthetic.dir is not set")
	}
	if err := common.ValidateIdentifier(spec.Name); err != nil {
		return nil, fmt.Errorf("synthetic: catalog name: %w", err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("synthetic: synthetic.dir: %w", err)
	}
	hash, err := build.SpecHash(syn)
	if err != nil {
		return nil, fmt.Errorf("synthetic: spec hash: %w", err)
	}
	d := &driver{
		name:     spec.Name,
		layout:   build.Layout{Dir: abs, Name: spec.Name},
		specHash: hash,
		path:     memoryDB,
		state:    StateNotBuilt,
	}
	id, err := d.layout.ReadCurrent()
	if err != nil {
		return nil, fmt.Errorf("synthetic %q: %w", spec.Name, err)
	}
	if id == "" {
		return d, nil
	}
	// Like duckdb_file, a snapshot that current names must exist.
	path := d.layout.Snapshot(id)
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("synthetic %q: snapshot of build %s: %w", spec.Name, id, err)
	}
	rep, err := d.layout.ReadReport(id)
	if err != nil {
		return nil, fmt.Errorf("synthetic %q: report of build %s: %w", spec.Name, id, err)
	}
	d.path = path
	d.state = d.stateFor(rep.SpecHash)
	return d, nil
}

func (d *driver) stateFor(specHash string) string {
	if specHash == d.specHash {
		return StateReady
	}
	return StateStale
}

// Name implements pkgds.DataSource.
func (d *driver) Name() string { return d.name }

// Type implements pkgds.DataSource.
func (d *driver) Type() string { return Type }

// Readonly implements pkgds.DataSource.
func (d *driver) Readonly() bool { return true }

// State implements pkgds.StateReporter.
func (d *driver) State() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}

// ExternalPaths implements pkgds.ExternalPather. The trailing separator
// makes it a folder prefix in allowed_directories, so a swap can attach
// a new snapshot after the hardening lock. The reports stay outside.
func (d *driver) ExternalPaths() []string {
	return []string{d.layout.SnapshotsDir() + string(filepath.Separator)}
}

// Attach attaches the current snapshot read-only. Without a snapshot it
// attaches an empty in-memory database: DuckDB refuses a read-only
// in-memory database, and user SQL cannot write anyway.
func (d *driver) Attach(ctx context.Context, conn *sql.Conn, _ pkgds.AttachOptions) error {
	d.mu.Lock()
	path := d.path
	d.mu.Unlock()
	if err := attach(ctx, conn, d.name, path); err != nil {
		if common.IsAlreadyAttached(err) {
			return nil
		}
		return fmt.Errorf("synthetic: ATTACH %s: %w", d.name, err)
	}
	return nil
}

func attach(ctx context.Context, conn *sql.Conn, name, path string) error {
	stmt := fmt.Sprintf(`ATTACH '%s' AS %s`, memoryDB, name)
	if path != memoryDB {
		stmt = fmt.Sprintf(`ATTACH '%s' AS %s (TYPE DUCKDB, READ_ONLY)`, common.EscapeSQLString(path), name)
	}
	_, err := conn.ExecContext(ctx, stmt)
	return err
}

// Swap replaces the attached catalog with the snapshot of buildID and
// sets the state from specHash, the spec hash of that build. ATTACH and
// DETACH change the whole DuckDB instance, so the caller runs Swap
// through executor.Exclusive. When the new ATTACH fails, the old
// database is attached again and the error is returned.
func (d *driver) Swap(ctx context.Context, conn *sql.Conn, buildID, specHash string) error {
	if !build.ValidBuildID(buildID) {
		return fmt.Errorf("synthetic %q: %w", d.name, build.ErrInvalidBuildID)
	}
	path := d.layout.Snapshot(buildID)
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, err := conn.ExecContext(ctx, "DETACH DATABASE IF EXISTS "+d.name); err != nil {
		return fmt.Errorf("synthetic %q: DETACH: %w", d.name, err)
	}
	if err := attach(ctx, conn, d.name, path); err != nil {
		if rerr := attach(ctx, conn, d.name, d.path); rerr != nil {
			// Retention may have deleted the old file once it was detached.
			// An empty catalog keeps the name resolvable and the state honest.
			if merr := attach(ctx, conn, d.name, memoryDB); merr == nil {
				d.path = memoryDB
				d.state = StateNotBuilt
			}
			return fmt.Errorf("synthetic %q: ATTACH build %s: %w (attach of the old database: %w)", d.name, buildID, err, rerr)
		}
		return fmt.Errorf("synthetic %q: ATTACH build %s: %w", d.name, buildID, err)
	}
	d.path = path
	d.state = d.stateFor(specHash)
	return nil
}

// Schema introspects information_schema for the attached catalog.
func (d *driver) Schema(ctx context.Context, conn *sql.Conn) (pkgds.Schema, error) {
	return common.IntrospectCatalog(ctx, conn, d.name)
}

// HealthCheck runs a cheap SELECT against the attached catalog. An empty
// in-memory catalog passes: it has the schema main.
func (d *driver) HealthCheck(ctx context.Context, conn *sql.Conn, opts pkgds.HealthOptions) error {
	q := opts.Query
	if q == "" {
		q = fmt.Sprintf(`SELECT 1 FROM information_schema.schemata WHERE catalog_name = '%s' LIMIT 1`,
			common.EscapeSQLString(d.name))
	}
	var one int
	if err := conn.QueryRowContext(ctx, q).Scan(&one); err != nil {
		return fmt.Errorf("synthetic: health: %w", err)
	}
	return nil
}

// Close clears the state. The attach lives on the DuckDB instance and
// ends when the pool is closed.
func (d *driver) Close() error {
	d.mu.Lock()
	d.path = memoryDB
	d.state = ""
	d.mu.Unlock()
	return nil
}
