// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/internal/synth/rng"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// sampleTable is the builder table that holds a profile sample. It lives
// in the default catalog of the builder instance.
const sampleTable = "sluice_sample"

// sampleHash names the row hash column of the sample query. A source
// column of that name is refused.
const sampleHash = "sluice_sample_row_hash"

// profileSource is what the build reads to profile one table (plan 1.2).
type profileSource struct {
	// table is the source table, or the sample when sampled.
	table profile.Table
	// sampled is true when table is the sample.
	sampled bool
	// rows is the exact row count of the source table, sizeRows the one
	// size uses (noisy under noise). Both are 0 when not sampled: then
	// the base query of the source gives them.
	rows, sizeRows int64
	// drop removes the sample. It is never nil.
	drop func()
}

// profileTable applies t.Profile (plan 1.2, 1.3). Without sampleRows,
// with size.rows 0, or when the table has at most sampleRows rows, it
// reads pt as before.
func (b *builder) profileTable(ctx context.Context, conn *sql.Conn, t *apitypes.SyntheticTable, pt profile.Table,
	infos []profile.ColumnInfo) (*profileSource, error) {
	src := &profileSource{table: pt, drop: func() {}}
	// A table with no rows reads no profile (tableBudget skips it too).
	if t.Profile == nil || t.Profile.SampleRows == nil || t.Size.Rows != nil && *t.Size.Rows == 0 {
		return src, nil
	}
	n := *t.Profile.SampleRows
	var rows int64
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM "+tableSQL(pt)).Scan(&rows); err != nil {
		return nil, b.duckErr("count the source rows", err)
	}
	size := rows
	if nf := b.noiseFor(t); nf != nil {
		size = profile.NoisyRows(rows, nf)
	}
	// Under noise the noisy count decides, so the choice does not show
	// the exact count.
	if size <= n {
		return src, nil
	}
	var catalog string
	if err := conn.QueryRowContext(ctx, "SELECT current_database()").Scan(&catalog); err != nil {
		return nil, b.duckErr("read the builder catalog", err)
	}
	sample := profile.Table{Catalog: catalog, Schema: "main", Name: sampleTable}
	cols := make([]string, len(infos))
	for i, c := range infos {
		if strings.EqualFold(c.Name, sampleHash) {
			return nil, errors.New("profile.sampleRows: the source has a column named " + sampleHash)
		}
		cols[i] = parser.QuoteIdent(c.Name)
	}
	// Each row is kept when its hash is below N/R of the hash range. One
	// row then changes only its own place in the sample, so the noise
	// sensitivity stays 1, and the sample does not depend on the scan
	// order or the thread count (plan 5, 1.3). The size is about N.
	// Equal rows have equal hashes, so the hash also takes the copy
	// number of the row. Equal rows are interchangeable, so it does not
	// matter which copy gets which number.
	r := rng.For(b.seed, b.name, t.Name, "", rng.PurposeSample)
	threshold := uint64(math.MaxUint64)
	if f := float64(n) / float64(size); f < 1 {
		threshold = uint64(f * math.MaxUint64)
	}
	q := "CREATE OR REPLACE TABLE " + tableSQL(sample) + " AS SELECT * EXCLUDE (" + sampleHash + ") FROM (" +
		"SELECT *, hash(" + strings.Join(cols, ", ") + ") AS " + sampleHash + " FROM " + tableSQL(pt) + ")" +
		" QUALIFY hash(CAST(" + strconv.FormatUint(r.Uint64(), 10) + " AS UBIGINT), " + sampleHash +
		", row_number() OVER (PARTITION BY " + sampleHash + ")) < CAST(" + strconv.FormatUint(threshold, 10) + " AS UBIGINT)"
	if _, err := conn.ExecContext(ctx, q); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		// The error text can quote the query, which holds the seed values.
		b.log.Debug("synthetic build: sample query failed", "table", t.Name)
		return nil, errors.New("cannot create the profile sample")
	}
	src.table, src.sampled, src.rows, src.sizeRows = sample, true, rows, size
	src.drop = func() {
		// A new context: the build context may be canceled already.
		if _, err := conn.ExecContext(context.WithoutCancel(ctx), "DROP TABLE IF EXISTS "+tableSQL(sample)); err != nil {
			b.log.Debug("synthetic build: drop the profile sample failed", "table", t.Name)
		}
	}
	return src, nil
}

func tableSQL(t profile.Table) string {
	return parser.QuoteIdent(t.Catalog) + "." + parser.QuoteIdent(t.Schema) + "." + parser.QuoteIdent(t.Name)
}
