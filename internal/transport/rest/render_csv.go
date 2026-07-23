// SPDX-License-Identifier: AGPL-3.0-or-later

package rest

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/bino-bi/sluice/internal/queryservice"
)

// renderCSV streams the result as RFC 4180 CSV with a header row. Row
// count and truncation are not encoded in the body (CSV has no place for
// them); callers get them via the X-Sluice-Row-Count / X-Sluice-Truncated
// HTTP trailers, declared before the first body write and set once the
// stream is drained.
func renderCSV(w http.ResponseWriter, res *queryservice.QueryResult) error {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Trailer", "X-Sluice-Row-Count, X-Sluice-Truncated")
	flusher, _ := w.(http.Flusher)
	writer := csv.NewWriter(w)

	header := make([]string, len(res.Columns))
	for i, c := range res.Columns {
		header[i] = c.Name
	}
	if err := writer.Write(header); err != nil {
		return err
	}

	scan := make([]any, len(res.Columns))
	ptrs := make([]any, len(res.Columns))
	for i := range scan {
		ptrs[i] = &scan[i]
	}
	line := make([]string, len(res.Columns))
	for res.Rows.Next() {
		if err := res.Rows.Scan(ptrs...); err != nil {
			return err
		}
		for i, v := range scan {
			line[i] = csvString(v)
		}
		if err := writer.Write(line); err != nil {
			return err
		}
	}
	if err := res.Rows.Err(); err != nil {
		return err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return err
	}
	w.Header().Set("X-Sluice-Row-Count", strconv.FormatInt(rowCount(res), 10))
	w.Header().Set("X-Sluice-Truncated", strconv.FormatBool(res.Truncated))
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}

// csvString renders a database/sql value as a string column. Nil renders
// as an empty field (matching DuckDB's default COPY behaviour).
func csvString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	case string:
		return x
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int, int64, int32, int16, int8,
		uint, uint64, uint32, uint16, uint8,
		float32, float64:
		return fmt.Sprintf("%v", x)
	default:
		return fmt.Sprintf("%v", v)
	}
}
