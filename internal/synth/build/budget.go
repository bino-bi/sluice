// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/synth/generate"
	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/internal/synth/rng"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// worstCase makes every default take its longest fallback chain.
var worstCase = profile.Counts{Rows: math.MaxInt64, NonNull: math.MaxInt64, Distinct: 0}

// planNoise plans the budget, computes the source fingerprint and starts
// the noise report. It runs before the first noisy draw.
func (b *builder) planNoise(ctx context.Context, conn *sql.Conn, epsilon float64) error {
	m, err := b.planBudget(ctx, conn)
	if err != nil {
		return err
	}
	if b.fingerprint, err = b.sourceFingerprint(ctx, conn); err != nil {
		return err
	}
	b.epsM = epsilon / float64(m)
	b.rep.Noise = &NoiseReport{Epsilon: epsilon, Measurements: m, EpsilonPerMeasurement: b.epsM}
	b.log.Info("synthetic build: noise budget", slog.String("name", b.name), slog.Float64("epsilon", epsilon),
		slog.Int("measurements", m), slog.Float64("epsilon_per_measurement", b.epsM))
	return nil
}

// planBudget counts the noisy measurements of the build (plan 1.4, 6.2).
// It reads only information_schema and the tags, and resolves with
// worst-case counts. Its errors are the ones the build would give later.
func (b *builder) planBudget(ctx context.Context, conn *sql.Conn) (int, error) {
	m := 0
	for i := range b.spec.Tables {
		t := &b.spec.Tables[i]
		n, err := b.tableBudget(ctx, conn, t)
		if err != nil {
			return 0, fmt.Errorf("table %s: %w", t.Name, err)
		}
		m += n
	}
	return m, nil
}

func (b *builder) tableBudget(ctx context.Context, conn *sql.Conn, t *apitypes.SyntheticTable) (int, error) {
	// The maxRatio check still reads the noisy row count.
	if t.Size.Rows != nil && *t.Size.Rows == 0 {
		return 1, nil
	}
	schema, name, _ := strings.Cut(t.Name, ".")
	pt := profile.Table{Catalog: b.spec.Source, Schema: schema, Name: name}
	infos, err := b.sourceColumns(ctx, conn, pt)
	if err != nil {
		return 0, err
	}
	if len(infos) == 0 {
		return 0, errors.New("not found in the source")
	}
	resolver, err := generate.NewResolver(b.spec, t)
	if err != nil {
		return 0, err
	}
	sibs := make(map[string]generate.Sibling, len(infos))
	for i, c := range infos {
		sibs[strings.ToLower(c.Name)] = generate.Sibling{Index: i, Name: c.Name, Type: c.Type}
	}
	m := 1 // rows
	var errs []error
	cols := make([]*column, len(infos))
	for i, info := range infos {
		c := &column{info: info, sibs: sibs}
		cols[i] = c
		if b.in.Tagger != nil {
			c.tags = b.in.Tagger.Tags(pt.Catalog, pt.Schema, pt.Name, info.Name)
		}
		gcol := b.genColumn(ctx, conn, c, 0)
		choice, err := resolver.Resolve(gcol, worstCase, c.tags)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		c.choice = choice
		m++ // nulls
		for ch := &choice; ch != nil; ch = ch.Fallback {
			if _, ok := ch.Args["by"]; ok {
				errs = append(errs, fmt.Errorf("column %s: by is not supported with noise", info.Name))
				break
			}
			g, ok := generate.Lookup(ch.Generator)
			if !ok {
				errs = append(errs, fmt.Errorf("column %s: unknown generator %q", info.Name, ch.Generator))
				break
			}
			needs := g.Needs(gcol, ch.Args)
			n, ok := profile.Measurements(needs)
			if !ok {
				errs = append(errs, fmt.Errorf("column %s: generator %s is not supported with noise", info.Name, ch.Generator))
				break
			}
			if ch == &choice && choice.Privacy != nil && choice.Privacy.Bounds != nil && !needs.ReadsNumbers() {
				errs = append(errs, fmt.Errorf("column %s: privacy.bounds needs a generator that reads numbers", info.Name))
			}
			m += n
		}
	}
	if len(errs) > 0 {
		return 0, errors.Join(errs...)
	}
	for _, g := range correlatedGroups(cols) {
		m += len(g) * (len(g) - 1) / 2
	}
	return m, nil
}

// sourceFingerprint hashes count(*) and sum(hash(all columns)) of every
// table of the spec, in list order (plan 1.9). The sum of UBIGINT is an
// exact HUGEINT, so the result does not depend on the row order. It is
// never logged or stored.
func (b *builder) sourceFingerprint(ctx context.Context, conn *sql.Conn) ([32]byte, error) {
	h := sha256.New()
	for i := range b.spec.Tables {
		t := &b.spec.Tables[i]
		schema, name, _ := strings.Cut(t.Name, ".")
		pt := profile.Table{Catalog: b.spec.Source, Schema: schema, Name: name}
		infos, err := b.sourceColumns(ctx, conn, pt)
		if err != nil {
			return [32]byte{}, err
		}
		cols := make([]string, len(infos))
		for j, c := range infos {
			cols[j] = parser.QuoteIdent(c.Name)
		}
		from := parser.QuoteIdent(pt.Catalog) + "." + parser.QuoteIdent(pt.Schema) + "." + parser.QuoteIdent(pt.Name)
		q := "SELECT count(*), CAST(coalesce(sum(hash(" + strings.Join(cols, ", ") + ")), 0) AS VARCHAR) FROM " + from
		var n int64
		var sum string
		if err := conn.QueryRowContext(ctx, q).Scan(&n, &sum); err != nil {
			return [32]byte{}, b.duckErr("read the source fingerprint", err)
		}
		hashString(h, t.Name)
		hashString(h, strconv.FormatInt(n, 10))
		hashString(h, sum)
	}
	var f [32]byte
	copy(f[:], h.Sum(nil))
	return f, nil
}

// noiseFor returns the noise of one column of table t ("" is the table),
// nil without noise.
func (b *builder) noiseFor(t *apitypes.SyntheticTable) func(column string) *profile.Noise {
	if b.epsM == 0 {
		return nil
	}
	fp := hex.EncodeToString(b.fingerprint[:])
	return func(column string) *profile.Noise {
		return &profile.Noise{Epsilon: b.epsM, Stream: func(m string) *rng.Stream {
			return rng.For(b.seed, b.name, t.Name, column, rng.PurposeNoise+"/"+m+"/"+fp)
		}}
	}
}

// columnNoise is noiseFor(t)(column), nil without noise.
func (b *builder) columnNoise(t *apitypes.SyntheticTable, column string) *profile.Noise {
	if nf := b.noiseFor(t); nf != nil {
		return nf(column)
	}
	return nil
}
