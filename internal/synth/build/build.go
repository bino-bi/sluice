// SPDX-License-Identifier: AGPL-3.0-or-later

// Package build builds a synthetic snapshot: it profiles the source in a
// short-lived DuckDB instance, filters the profile, generates the rows,
// writes them to a new DuckDB file with a report, and publishes both.
package build

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"strings"
	"time"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/datasource"
	"github.com/bino-bi/sluice/internal/datasource/drivers/common"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/synth/generate"
	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/internal/synth/rng"
	"github.com/bino-bi/sluice/internal/version"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

// DefaultMaxRatio applies when Input.MaxRatio is 0.
const DefaultMaxRatio = 0.2

const (
	// minSeedBytes: with a short, guessable seed the jitter is known and
	// the true bounds follow from the bin edges.
	minSeedBytes = 16
	batchRows    = 10000
	// outCatalog cannot clash with a source name: RFC 1123 has no "_".
	outCatalog = "sluice_out"
	stageTable = "sluice_stage"
)

// ErrCanceled is the error of a build whose context was canceled.
var ErrCanceled = errors.New("canceled")

// Tagger answers the DataClassification tags of a source column.
// *policy.ColumnTagger implements it.
type Tagger interface {
	Tags(catalog, schema, table, column string) []string
}

// Input is what one build needs.
type Input struct {
	// DataSource is the synthetic data source, Source the one it reads.
	DataSource *apitypes.DataSource
	Source     *apitypes.DataSource
	// Secrets resolves the seed and the source's secrets.
	Secrets pkgds.SecretResolver
	// Tagger gives the tags for rules and strictTags. Nil means no tags.
	Tagger Tagger
	// Dir is synthetic.dir.
	Dir string
	// MaxRatio is synthetic.maxRatio. Zero means DefaultMaxRatio.
	MaxRatio float64
	// Harden tunes the builder instance (memory, threads, temp folder).
	Harden executor.HardenConfig
	// Origin is cli, admin or startup. It goes into the report.
	Origin string
	// BuildID is the id of this build. Empty makes a new one. The service
	// sets it, so its start audit record names the build.
	BuildID string
	// Clock returns now. Nil uses time.Now.
	Clock func() time.Time
	// Log gets the build log. Raw DuckDB errors go there at debug level.
	// Nil uses slog.Default.
	Log *slog.Logger
	// Audit, when set, returns the record stored in the report's audit
	// field. It runs each time the report is written. The CLI uses it,
	// because it does not write to the audit log.
	Audit func(*Report) *audit.Record
}

// Run builds one snapshot and publishes it. It returns the report also
// when the build fails, as soon as the build id exists; the report is
// then written with result failed and current is not changed.
func Run(ctx context.Context, in Input) (*Report, error) {
	if in.DataSource == nil || in.DataSource.Spec.Synthetic == nil || in.Source == nil {
		return nil, errors.New("synthetic build: data source, spec and source are required")
	}
	if in.Secrets == nil {
		return nil, errors.New("synthetic build: no secret resolver")
	}
	if in.Clock == nil {
		in.Clock = time.Now
	}
	if in.Log == nil {
		in.Log = slog.Default()
	}
	if in.MaxRatio == 0 {
		in.MaxRatio = DefaultMaxRatio
	}
	spec := in.DataSource.Spec.Synthetic
	specHash, err := SpecHash(spec)
	if err != nil {
		return nil, fmt.Errorf("synthetic build: spec hash: %w", err)
	}
	start := in.Clock()
	id := in.BuildID
	if id == "" {
		id, err = newBuildID(start)
		if err != nil {
			return nil, fmt.Errorf("synthetic build: build id: %w", err)
		}
	} else if !ValidBuildID(id) {
		return nil, fmt.Errorf("synthetic build: %w", ErrInvalidBuildID)
	}
	l := Layout{Dir: in.Dir, Name: in.DataSource.Metadata.Name}
	if err := l.mkdirs(); err != nil {
		return nil, fmt.Errorf("synthetic build: %w", err)
	}
	b := &builder{
		in:   in,
		spec: spec,
		name: l.Name,
		l:    l,
		log:  in.Log,
		hash: sha256.New(),
		rep: &Report{
			Version:       ReportVersion,
			BuildID:       id,
			Name:          l.Name,
			Source:        spec.Source,
			Origin:        in.Origin,
			SpecHash:      specHash,
			Algorithm:     Algorithm,
			SluiceVersion: version.Current().Version,
			StartedAt:     start.UTC(),
			Tables:        []TableReport{},
			Spikes:        []SpikeReport{},
			Warnings:      []string{},
		},
	}
	err = b.run(ctx)
	if err == nil {
		return b.rep, nil
	}
	if ctx.Err() != nil {
		err = fmt.Errorf("%w: %w", ErrCanceled, ctx.Err())
	}
	b.cleanup()
	b.finish(ResultFailed, err)
	if werr := b.writeReport(); werr != nil {
		b.log.Error("synthetic build: cannot write the report", "name", l.Name, "build_id", id, "error", werr)
	}
	return b.rep, err
}

type builder struct {
	in   Input
	spec *apitypes.SyntheticSpec
	name string
	l    Layout
	log  *slog.Logger
	rep  *Report
	seed []byte
	hash hash.Hash
	// renamed is set once the snapshot has its final name.
	renamed bool
	// exprs are the checked expression columns by lower-case table and
	// column name.
	exprs map[string]map[string]exprColumn
	// keys holds the parent keys of the foreignKey columns.
	keys *keyCache
}

func (b *builder) finish(result string, err error) {
	now := b.in.Clock()
	b.rep.Result = result
	b.rep.Error = ""
	if err != nil {
		b.rep.Error = err.Error()
	}
	b.rep.FinishedAt = now.UTC()
	b.rep.DurationMs = now.Sub(b.rep.StartedAt).Milliseconds()
}

func (b *builder) writeReport() error {
	if b.in.Audit != nil {
		b.rep.Audit = b.in.Audit(b.rep)
	}
	return b.l.writeReport(b.rep)
}

// cleanup removes the files of a failed build. The report stays.
func (b *builder) cleanup() {
	tmp := b.l.tempSnapshot(b.rep.BuildID)
	for _, p := range []string{tmp, tmp + ".wal"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			b.log.Warn("synthetic build: cannot remove a temp file", "path", p, "error", err)
		}
	}
	if b.renamed {
		_ = os.Remove(b.l.Snapshot(b.rep.BuildID))
	}
}

func (b *builder) run(ctx context.Context) error {
	if b.spec.Privacy != nil && b.spec.Privacy.Noise != nil {
		return errors.New("privacy.noise is not supported yet")
	}
	if !strings.HasPrefix(b.spec.Seed, "secret://") {
		return errors.New("seed: must be a secret reference")
	}
	seed, err := b.in.Secrets.Resolve(ctx, b.spec.Seed)
	if err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	if len(seed) < minSeedBytes {
		return fmt.Errorf("seed: the secret has fewer than %d bytes", minSeedBytes)
	}
	b.seed = seed

	tmp := b.l.tempSnapshot(b.rep.BuildID)
	conn, closeAll, err := b.open(ctx, tmp)
	if err != nil {
		return err
	}
	defer closeAll()
	if err := conn.QueryRowContext(ctx, "SELECT version()").Scan(&b.rep.DuckDBVersion); err != nil {
		return b.duckErr("read the DuckDB version", err)
	}
	if err := b.checkExpressions(ctx, conn); err != nil {
		return err
	}
	// Parents first, so a child reads the final parent keys.
	order, err := apitypes.SyntheticTableOrder(b.spec)
	if err != nil {
		return err
	}
	b.keys = newKeyCache(b.spec)
	runner := &profile.Runner{Conn: conn, Log: b.log}
	for _, i := range order {
		if err := b.table(ctx, conn, runner, &b.spec.Tables[i]); err != nil {
			return err
		}
	}
	// DETACH writes a checkpoint, so the file holds every row.
	if _, err := conn.ExecContext(ctx, "DETACH "+outCatalog); err != nil {
		return b.duckErr("close the snapshot", err)
	}
	if _, err := os.Stat(tmp + ".wal"); err == nil {
		return errors.New("snapshot: a write-ahead log is left after detach")
	}
	if err := syncFile(tmp); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	if err := os.Rename(tmp, b.l.Snapshot(b.rep.BuildID)); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	b.renamed = true
	syncDir(b.l.SnapshotsDir())

	b.rep.ContentHash = "sha256:" + hex.EncodeToString(b.hash.Sum(nil))
	b.finish(ResultSucceeded, nil)
	if err := b.writeReport(); err != nil {
		return fmt.Errorf("report: %w", err)
	}
	return b.l.publish(b.rep.BuildID)
}

// open starts the builder instance (plan 1.3): the source attached
// read-only under its own name and the output file as sluice_out, then
// the normal hardening. It returns the one connection of the instance.
func (b *builder) open(ctx context.Context, tmp string) (*sql.Conn, func(), error) {
	reg, err := datasource.New(ctx, datasource.Options{
		Snapshot:       &datasource.Snapshot{DataSources: []*apitypes.DataSource{b.in.Source}},
		Secrets:        b.in.Secrets,
		Logger:         b.log,
		HealthInterval: -1,
		FailFast:       true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("source %s: %w", b.spec.Source, err)
	}
	harden := b.in.Harden
	harden.AllowedExternalPaths = append(append([]string(nil), harden.AllowedExternalPaths...), reg.ExternalPaths()...)
	attachSource := reg.AttachHook()
	hook := func(ctx context.Context, conn *sql.Conn) error {
		// Weekdays and months of TIMESTAMPTZ, and the cast of the staged
		// UTC timestamps, must not depend on the host time zone.
		if _, err := conn.ExecContext(ctx, "SET TimeZone = 'UTC'"); err != nil {
			return err
		}
		if err := attachSource(ctx, conn); err != nil {
			return err
		}
		_, err := conn.ExecContext(ctx, "ATTACH '"+common.EscapeSQLString(tmp)+"' AS "+outCatalog+" (TYPE DUCKDB)")
		if common.IsAlreadyAttached(err) {
			return nil
		}
		return err
	}
	// One connection: the time zone setting and the staging table belong
	// to it, and a second connection would attach the output again.
	exec, err := executor.New(ctx, executor.Config{
		Harden:     harden,
		AttachHook: hook,
		MaxOpen:    1,
		MaxIdle:    1,
		Logger:     b.log,
	})
	if err != nil {
		_ = reg.Close()
		return nil, nil, b.duckErr("open the builder instance", err)
	}
	conn, err := exec.DB().Conn(ctx)
	if err != nil {
		_ = exec.Close()
		_ = reg.Close()
		return nil, nil, b.duckErr("open the builder instance", err)
	}
	return conn, func() {
		_ = conn.Close()
		_ = exec.Close()
		_ = reg.Close()
	}, nil
}

// duckErr keeps the raw DuckDB text, which may quote a value, in the
// debug log.
func (b *builder) duckErr(what string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	b.log.Debug("synthetic build: duckdb error", "name", b.name, "step", what, "error", err)
	return errors.New("cannot " + what)
}

// column is one column of a table in the build.
type column struct {
	info     profile.ColumnInfo
	counts   profile.Counts
	tags     []string
	choice   generate.Choice
	settings profile.Settings
	prof     *profile.Column
	gen      generate.ColumnGen
	stage    generate.Stage
	snapType string
	warnings []string
	// sibs are the columns of the table by lower-case name, shared by all
	// columns of the table.
	sibs map[string]generate.Sibling
	// refs are the lower-case names of the columns this one reads.
	refs []string
	// by holds the group counts of a column with by, nil otherwise.
	by *profile.GroupStats
	// expr is set for an expression column.
	expr *exprColumn
	// keys are the parent keys of a foreignKey column. keyOut collects the
	// staged values of a column that a foreignKey reads.
	keys, keyOut *generate.KeySet
}

func (b *builder) table(ctx context.Context, conn *sql.Conn, runner *profile.Runner, t *apitypes.SyntheticTable) error {
	fail := func(err error) error { return fmt.Errorf("table %s: %w", t.Name, err) }
	schema, name, _ := strings.Cut(t.Name, ".")
	pt := profile.Table{Catalog: b.spec.Source, Schema: schema, Name: name}

	infos, err := b.sourceColumns(ctx, conn, pt)
	if err != nil {
		return fail(err)
	}
	if len(infos) == 0 {
		return fail(errors.New("not found in the source"))
	}
	resolver, err := generate.NewResolver(b.spec, t)
	if err != nil {
		return fail(err)
	}
	names := make([]string, len(infos))
	for i, c := range infos {
		names[i] = c.Name
	}
	if missing := resolver.MissingColumns(names); len(missing) > 0 {
		return fail(fmt.Errorf("columns %s are not in the source table", strings.Join(missing, ", ")))
	}
	exact, err := exactColumns(t, infos)
	if err != nil {
		return fail(err)
	}
	if t.Size.Ratio != nil && *t.Size.Ratio > b.in.MaxRatio {
		return fail(fmt.Errorf("size.ratio %g is above synthetic.maxRatio %g", *t.Size.Ratio, b.in.MaxRatio))
	}
	counts, err := runner.Base(ctx, pt, infos)
	if err != nil {
		return err
	}
	srcRows := counts[0].Rows
	rows, ratio, err := b.size(t.Size, srcRows)
	if err != nil {
		return fail(err)
	}
	tr := TableReport{Name: t.Name, SourceRows: srcRows, SyntheticRows: rows, Ratio: ratio, Columns: []ColumnReport{},
		DependsOn: tableDeps(b.spec, t)}

	sibs := make(map[string]generate.Sibling, len(infos))
	for i, info := range infos {
		sibs[strings.ToLower(info.Name)] = generate.Sibling{Index: i, Name: info.Name, Type: info.Type}
	}
	cols := make([]*column, len(infos))
	for i, info := range infos {
		cols[i] = &column{info: info, counts: counts[i], tags: []string{}, snapType: snapshotType(info.Type, ""), sibs: sibs}
		if b.keys.wanted(t.Name, info.Name) {
			cols[i].keyOut = &generate.KeySet{}
		}
	}
	var order []int
	if rows > 0 {
		if err := b.resolve(ctx, conn, runner, t, pt, resolver, cols, rows); err != nil {
			return fail(err)
		}
		for _, c := range cols {
			if c.choice.Generator == apitypes.GenExpression {
				e, ok := b.exprs[strings.ToLower(t.Name)][strings.ToLower(c.info.Name)]
				if !ok {
					return fail(fmt.Errorf("column %s: expression was not checked", c.info.Name))
				}
				c.expr = &e
			}
		}
		if order, err = evalOrder(cols); err != nil {
			return fail(err)
		}
	}
	b.hashTable(t.Name, cols)
	if rows > 0 {
		if err := b.stage(ctx, conn, cols, order, rows, len(exact) > 0); err != nil {
			return fail(err)
		}
		if len(exact) > 0 {
			if tr.ExactMatches, err = b.rejectExact(ctx, conn, pt, cols, order, exact, rows); err != nil {
				return fail(err)
			}
		}
	}
	for _, c := range cols {
		if c.keyOut != nil {
			b.keys.put(t.Name, c)
		}
	}
	if err := b.write(ctx, conn, schema, name, cols, rows > 0); err != nil {
		return fail(err)
	}
	if rows > 0 {
		target := outCatalog + "." + parser.QuoteIdent(schema) + "." + parser.QuoteIdent(name)
		if err := b.verifyForeignKeys(ctx, conn, target, cols); err != nil {
			return fail(err)
		}
	}
	for _, c := range cols {
		cr, err := b.columnReport(ctx, conn, pt, t, c, rows)
		if err != nil {
			return fail(err)
		}
		tr.Columns = append(tr.Columns, cr)
	}
	b.rep.Tables = append(b.rep.Tables, tr)
	return nil
}

func (b *builder) sourceColumns(ctx context.Context, conn *sql.Conn, t profile.Table) ([]profile.ColumnInfo, error) {
	rows, err := conn.QueryContext(ctx,
		"SELECT column_name, data_type FROM information_schema.columns "+
			"WHERE table_catalog = ? AND table_schema = ? AND table_name = ? ORDER BY ordinal_position",
		t.Catalog, t.Schema, t.Name)
	if err != nil {
		return nil, b.duckErr("read the source columns", err)
	}
	defer func() { _ = rows.Close() }()
	var out []profile.ColumnInfo
	for rows.Next() {
		var c profile.ColumnInfo
		if err := rows.Scan(&c.Name, &c.Type); err != nil {
			return nil, b.duckErr("read the source columns", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, b.duckErr("read the source columns", err)
	}
	return out, nil
}

// size applies plan 1.11 and returns the row count and the ratio.
func (b *builder) size(s apitypes.SyntheticSize, srcRows int64) (int64, float64, error) {
	if s.Ratio != nil {
		return int64(math.Floor(*s.Ratio * float64(srcRows))), *s.Ratio, nil
	}
	if s.Rows == nil {
		return 0, 0, errors.New("size needs ratio or rows")
	}
	n := *s.Rows
	if n > int64(math.Floor(b.in.MaxRatio*float64(srcRows))) {
		return 0, 0, fmt.Errorf("size.rows is above synthetic.maxRatio %g of the source rows", b.in.MaxRatio)
	}
	ratio := 0.0
	if srcRows > 0 {
		ratio = float64(n) / float64(srcRows)
	}
	return n, ratio, nil
}

// snapshotType is the column type in the snapshot (plan 1.14): the
// source type, except VARCHAR for bucket and for any type with an ENUM,
// whose labels would otherwise show in the catalog. An ENUM nested in a
// STRUCT, MAP or UNION is KindOther, so its column is NULL anyway.
func snapshotType(dataType string, g apitypes.SyntheticGenerator) string {
	if g == apitypes.GenBucket || strings.Contains(strings.ToUpper(dataType), "ENUM(") {
		return "VARCHAR"
	}
	return dataType
}

// resolve picks the generator of every column, then reads its profile
// and builds its value source. Choice errors of all columns come back
// together, so one run shows every missing choice.
func (b *builder) resolve(ctx context.Context, conn *sql.Conn, runner *profile.Runner, t *apitypes.SyntheticTable,
	pt profile.Table, resolver *generate.Resolver, cols []*column, rows int64) error {
	var errs []error
	for _, c := range cols {
		if b.in.Tagger != nil {
			if tags := b.in.Tagger.Tags(pt.Catalog, pt.Schema, pt.Name, c.info.Name); len(tags) > 0 {
				c.tags = tags
			}
		}
		choice, err := resolver.Resolve(b.genColumn(ctx, conn, c, rows), c.counts, c.tags)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if choice.Generator == apitypes.GenCorrelated {
			errs = append(errs, fmt.Errorf("column %s: correlated is not supported yet", c.info.Name))
			continue
		}
		c.choice = choice
	}
	errs = append(errs, checkByBucket(cols)...)
	errs = append(errs, b.checkForeignKeys(cols)...)
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	for _, c := range cols {
		if err := b.prepare(ctx, conn, runner, t, pt, c, rows); err != nil {
			return err
		}
	}
	return nil
}

func (b *builder) genColumn(ctx context.Context, conn *sql.Conn, c *column, rows int64) generate.Column {
	typ := c.info.Type
	return generate.Column{
		Name:     c.info.Name,
		Type:     typ,
		Rows:     rows,
		Siblings: c.sibs,
		Keys:     c.keys,
		Cast: func(v string) (any, error) {
			var out any
			if err := conn.QueryRowContext(ctx, "SELECT CAST(? AS "+typ+")", v).Scan(&out); err != nil {
				b.log.Debug("synthetic build: cast failed", "column", c.info.Name, "error", err)
				return nil, errors.New("does not cast to the column type")
			}
			return out, nil
		},
	}
}

// checkByBucket refuses a by column whose generator is bucket: its
// labels are ranges, the source groups are values, so no row would match.
func checkByBucket(cols []*column) []error {
	var errs []error
	for _, c := range cols {
		by, err := generate.BySiblings(generate.Column{Siblings: c.sibs}, c.choice.Args)
		if err != nil {
			continue
		}
		for _, s := range by {
			if cols[s.Index].choice.Generator == apitypes.GenBucket {
				errs = append(errs, fmt.Errorf("column %s: by column %s uses bucket, its labels do not match the source values", c.info.Name, s.Name))
			}
		}
	}
	return errs
}

// prepare reads the profile of one column and builds its value source.
// A default choice falls back along its chain when the profile or the
// generator fails (plan, step C notes).
func (b *builder) prepare(ctx context.Context, conn *sql.Conn, runner *profile.Runner, t *apitypes.SyntheticTable,
	pt profile.Table, c *column, rows int64) error {
	gcol := b.genColumn(ctx, conn, c, rows)
	stream := func(purpose string) *rng.Stream {
		return rng.For(b.seed, b.name, t.Name, c.info.Name, purpose)
	}
	choice := c.choice
	for {
		g, ok := generate.Lookup(choice.Generator)
		if !ok {
			return fmt.Errorf("column %s: unknown generator %q", c.info.Name, choice.Generator)
		}
		settings := profile.Effective(choice.Privacy, t.Privacy, b.spec.Privacy)
		// A template or an expression has no profile: without one its null
		// rate defaults to 0. A template's NULLs come from NULL refs only.
		var prof, genProf *profile.Column
		var err error
		needs := g.Needs(gcol, choice.Args)
		if choice.Generator == apitypes.GenTemplate || choice.Generator == apitypes.GenExpression {
			prof = &profile.Column{}
		} else {
			prof, err = runner.Column(ctx, pt, c.info, c.counts, needs, settings, stream(rng.PurposeJitter))
			genProf = prof
		}
		var by []generate.Sibling
		if err == nil {
			by, err = generate.BySiblings(gcol, choice.Args)
		}
		var groups []profile.Group
		var stats profile.GroupStats
		if err == nil && len(by) > 0 {
			infos := make([]profile.ColumnInfo, len(by))
			for i, s := range by {
				infos[i] = profile.ColumnInfo{Name: s.Name, Type: s.Type}
			}
			groups, stats, err = runner.Groups(ctx, pt, c.info, infos, needs, settings, func(key string) *rng.Stream {
				return stream(rng.JitterBy(key))
			})
		}
		// The VARCHAR and integer default categorical is for columns
		// without rare values; with them the column falls back.
		kind := profile.KindOf(c.info.Type)
		if err == nil && choice.Fallback != nil && choice.Generator == apitypes.GenCategorical &&
			(kind == profile.KindText || kind == profile.KindInteger) && prof.TopValues.Rest > 0 {
			err = errors.New("rare values")
		}
		var gen generate.ColumnGen
		if err == nil {
			streams := generate.Streams{
				Values:  stream(rng.PurposeValues),
				Nulls:   stream(rng.PurposeNulls),
				Shuffle: stream(rng.PurposeShuffle),
				FanOut:  stream(rng.PurposeFanOut),
				Orphans: stream(rng.PurposeOrphans),
			}
			if len(by) > 0 {
				gen, err = generate.NewBy(choice.Generator, gcol, genProf, groups, choice.Args, streams)
			} else {
				gen, err = generate.New(choice.Generator, gcol, genProf, choice.Args, streams)
			}
		}
		if err == nil {
			c.choice, c.settings, c.prof, c.gen = choice, settings, prof, gen
			if len(by) > 0 {
				c.by = &stats
			}
			c.stage = generate.StageOf(gcol, choice.Generator)
			c.snapType = snapshotType(c.info.Type, choice.Generator)
			if choice.Warning != "" {
				c.warnings = append(c.warnings, choice.Warning)
			}
			return nil
		}
		if ctx.Err() != nil || choice.Fallback == nil {
			return err
		}
		b.log.Debug("synthetic build: default falls back", "column", c.info.Name,
			"from", string(choice.Generator), "to", string(choice.Fallback.Generator), "reason", err)
		choice = *choice.Fallback
	}
}

// hashTable starts the content hash of a table: its name and columns.
func (b *builder) hashTable(name string, cols []*column) {
	hashString(b.hash, name)
	for _, c := range cols {
		hashString(b.hash, c.info.Name)
		hashString(b.hash, c.snapType)
	}
}

// evalOrder sets the refs of every column and returns the order in
// which a row's columns are drawn.
func evalOrder(cols []*column) ([]int, error) {
	gcols := make([]generate.Column, len(cols))
	refs := make([][]string, len(cols))
	for i, c := range cols {
		gcols[i] = generate.Column{Name: c.info.Name}
		c.refs = apitypes.SyntheticColumn{Generator: c.choice.Generator, Args: c.choice.Args}.Refs()
		refs[i] = c.refs
	}
	return generate.Order(gcols, refs)
}

// stage creates the staging table and fills it row by row from one
// goroutine, so the rows and the content hash have a fixed order. The
// columns are drawn in order, then hashed in ordinal order, so the hash
// does not depend on the column graph.
// With rn the staging table gets the row index as rnColumn.
func (b *builder) stage(ctx context.Context, conn *sql.Conn, cols []*column, order []int, rows int64, rn bool) error {
	defs := make([]string, len(cols))
	for i, c := range cols {
		defs[i] = stageName(i) + " " + c.stage.SQL()
	}
	if rn {
		defs = append(defs, rnColumn+" BIGINT")
	}
	if _, err := conn.ExecContext(ctx, "CREATE OR REPLACE TABLE "+stageTable+" ("+strings.Join(defs, ", ")+")"); err != nil {
		return b.duckErr("create the staging table", err)
	}
	return appendRows(ctx, conn, rows, len(cols), rn, func(row *generate.RowCtx) error {
		for _, j := range order {
			v, err := cols[j].gen.Next(*row)
			if err != nil {
				return err
			}
			row.Values[j] = v
		}
		for j, v := range row.Values {
			if err := hashValue(b.hash, v); err != nil {
				return fmt.Errorf("column %s: %w", cols[j].info.Name, err)
			}
			if k := cols[j].keyOut; k != nil && v != nil {
				k.Values = append(k.Values, v)
			}
		}
		return nil
	}, b.duckErr)
}

func stageName(i int) string { return fmt.Sprintf("c%d", i) }

// write copies the staging table into sluice_out with the snapshot
// types. DuckDB does the casts. Without staged rows it only creates the
// table.
func (b *builder) write(ctx context.Context, conn *sql.Conn, schema, name string, cols []*column, staged bool) error {
	target := outCatalog + "." + parser.QuoteIdent(schema) + "." + parser.QuoteIdent(name)
	defs := make([]string, len(cols))
	for i, c := range cols {
		defs[i] = parser.QuoteIdent(c.info.Name) + " " + c.snapType
	}
	stmts := []string{
		"CREATE SCHEMA IF NOT EXISTS " + outCatalog + "." + parser.QuoteIdent(schema),
		"CREATE TABLE " + target + " (" + strings.Join(defs, ", ") + ")",
	}
	for _, s := range stmts {
		if _, err := conn.ExecContext(ctx, s); err != nil {
			return b.duckErr("create the snapshot table", err)
		}
	}
	if !staged {
		return nil
	}
	// preserve_insertion_order (on by default) keeps the staged row order.
	if _, err := conn.ExecContext(ctx, "INSERT INTO "+target+" "+snapshotSelect(cols)); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return b.castError(ctx, conn, cols, err)
	}
	if err := b.hashExpressions(ctx, conn, target, cols); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "DROP TABLE "+stageTable); err != nil {
		return b.duckErr("drop the staging table", err)
	}
	return nil
}

// castExpr casts a staged column to the snapshot type. TIME is staged as
// seconds of the day; DuckDB has no cast from a number to TIME.
func castExpr(staged, snapType string) string {
	if profile.KindOf(snapType) == profile.KindTime {
		return "TIME '00:00:00' + to_seconds(" + staged + ")"
	}
	return "CAST(" + staged + " AS " + snapType + ")"
}

// castError finds the column whose cast or expression failed, so the
// error can name it without the DuckDB text.
func (b *builder) castError(ctx context.Context, conn *sql.Conn, cols []*column, err error) error {
	b.log.Debug("synthetic build: snapshot insert failed", "name", b.name, "error", err)
	if cerr := b.castFailure(ctx, conn, cols); cerr != nil {
		return cerr
	}
	return errors.New("cannot write the snapshot table")
}

// castFailure returns the error of the first column whose cast or
// expression fails, nil when all succeed.
func (b *builder) castFailure(ctx context.Context, conn *sql.Conn, cols []*column) error {
	for i, c := range cols {
		if c.expr != nil {
			continue
		}
		var n int64
		q := "SELECT count(" + castExpr(stageName(i), c.snapType) + ") FROM " + stageTable
		if qerr := conn.QueryRowContext(ctx, q).Scan(&n); qerr != nil {
			return fmt.Errorf("column %s: a value does not cast to %s", c.info.Name, c.snapType)
		}
	}
	for i, c := range cols {
		if c.expr == nil {
			continue
		}
		var n int64
		q := "SELECT count(" + exprValue(i, c) + ") FROM (" + innerSelect(cols) + ") AS r"
		if qerr := conn.QueryRowContext(ctx, q).Scan(&n); qerr != nil {
			return fmt.Errorf("column %s: the expression fails or does not cast to %s", c.info.Name, c.snapType)
		}
	}
	return nil
}
