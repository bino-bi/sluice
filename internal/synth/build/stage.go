// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"fmt"
	"hash"
	"math"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/bino-bi/sluice/internal/synth/generate"
)

// appendRows appends rows rows to the staging table. fill sets the values
// of one row. With rn the row index goes into a last column. The appender
// flushes every batchRows rows.
func appendRows(ctx context.Context, conn *sql.Conn, rows int64, ncols int, rn bool, fill func(*generate.RowCtx) error,
	duckErr func(string, error) error) error {
	return conn.Raw(func(dc any) error {
		dconn, ok := dc.(driver.Conn)
		if !ok {
			return fmt.Errorf("unexpected driver connection %T", dc)
		}
		a, err := duckdb.NewAppender(dconn, "", "", stageTable)
		if err != nil {
			return duckErr("open the appender", err)
		}
		row := generate.RowCtx{Values: make([]any, ncols)}
		vals := make([]driver.Value, ncols, ncols+1)
		if rn {
			vals = vals[:ncols+1]
		}
		for i := range rows {
			row.Index = i
			if err := fill(&row); err != nil {
				_ = a.Close()
				return err
			}
			for j, v := range row.Values {
				vals[j] = v
			}
			if rn {
				vals[ncols] = i
			}
			if err := a.AppendRow(vals...); err != nil {
				_ = a.Close()
				return duckErr("append a staged row", err)
			}
			if (i+1)%batchRows == 0 {
				if err := a.Flush(); err != nil {
					_ = a.Close()
					return duckErr("append a staged row", err)
				}
				if err := ctx.Err(); err != nil {
					_ = a.Close()
					return err
				}
			}
		}
		if err := a.Close(); err != nil {
			return duckErr("append a staged row", err)
		}
		return nil
	})
}

// Value tags of the content hash encoding.
const (
	tagNull byte = iota
	tagInt
	tagFloat
	tagString
	tagBool
	tagTime
)

// hashString writes a length-prefixed string.
func hashString(h hash.Hash, s string) {
	var n [binary.MaxVarintLen64]byte
	h.Write(n[:binary.PutUvarint(n[:], uint64(len(s)))])
	h.Write([]byte(s))
}

// hashValue writes one staged value in a fixed binary encoding. The
// stage types are the only types a generator returns.
func hashValue(h hash.Hash, v any) error {
	var b [9]byte
	switch x := v.(type) {
	case nil:
		h.Write([]byte{tagNull})
	case int64:
		b[0] = tagInt
		binary.BigEndian.PutUint64(b[1:], uint64(x))
		h.Write(b[:])
	case float64:
		b[0] = tagFloat
		binary.BigEndian.PutUint64(b[1:], math.Float64bits(x))
		h.Write(b[:])
	case string:
		h.Write([]byte{tagString})
		hashString(h, x)
	case bool:
		b[0] = tagBool
		if x {
			b[1] = 1
		}
		h.Write(b[:2])
	case time.Time:
		b[0] = tagTime
		binary.BigEndian.PutUint64(b[1:], uint64(x.UnixMicro()))
		h.Write(b[:])
	default:
		return fmt.Errorf("unexpected value type %T", v)
	}
	return nil
}
