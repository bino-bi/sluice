// SPDX-License-Identifier: AGPL-3.0-or-later

package rest

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/bino-bi/sluice/internal/queryservice"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// renderJSON streams the result as a single JSON object:
//
//	{
//	  "query_id": "01H…",
//	  "columns":  ["id","name"],
//	  "rows":     [[1,"a"], [2,"b"]],
//	  "row_count": 2,
//	  "truncated": false
//	}
//
// Rows are streamed one-at-a-time so large result sets do not require a
// full in-memory buffer. We flush after the header section and then every
// N rows so clients see progress.
func renderJSON(w http.ResponseWriter, res *queryservice.QueryResult) error {
	const flushEvery = 100

	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	flusher, _ := w.(http.Flusher)

	// Header section: {"query_id":"…","columns":[…],"rows":[
	if _, err := fmt.Fprintf(w, `{"query_id":%q,"columns":`, res.QueryID); err != nil {
		return err
	}
	colNames := make([]string, len(res.Columns))
	for i, c := range res.Columns {
		colNames[i] = c.Name
	}
	if err := json.NewEncoder(w).Encode(colNames); err != nil {
		return err
	}
	// json.Encoder appends '\n'; the closing bracket / comma handling below
	// accounts for that. We write the array key on a fresh line.
	if _, err := w.Write([]byte(`,"rows":[`)); err != nil {
		return err
	}
	if flusher != nil {
		flusher.Flush()
	}

	// Rows.
	scan := make([]any, len(res.Columns))
	pointers := make([]any, len(res.Columns))
	for i := range scan {
		pointers[i] = &scan[i]
	}
	rowIdx := 0
	for res.Rows.Next() {
		if err := res.Rows.Scan(pointers...); err != nil {
			return err
		}
		row := make([]any, len(scan))
		for i, v := range scan {
			row[i] = jsonSafe(v)
		}
		if rowIdx > 0 {
			if _, err := w.Write([]byte{','}); err != nil {
				return err
			}
		}
		b, err := json.Marshal(row)
		if err != nil {
			return err
		}
		if _, err := w.Write(b); err != nil {
			return err
		}
		rowIdx++
		if flusher != nil && rowIdx%flushEvery == 0 {
			flusher.Flush()
		}
	}
	if err := res.Rows.Err(); err != nil {
		return err
	}

	// Trailer: closing rows array + row_count + truncated (+ warning).
	if _, err := fmt.Fprintf(w, `],"row_count":%d,"truncated":%t`,
		rowCount(res), res.Truncated); err != nil {
		return err
	}
	if res.Truncated {
		if _, err := fmt.Fprintf(w, `,"warning":{"code":%q,"message":%q}`,
			pkgerr.CodeResultTruncated, pkgerr.Message(pkgerr.CodeResultTruncated)); err != nil {
			return err
		}
	}
	if _, err := w.Write([]byte{'}'}); err != nil {
		return err
	}
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}

// jsonSafe converts database/sql-scanned values into JSON-friendly shapes.
// []byte becomes a string; everything else passes through.
func jsonSafe(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	default:
		return v
	}
}

// rowCount returns the cumulative count tracked on the iterator, or 0
// before Close.
func rowCount(res *queryservice.QueryResult) int64 {
	if res.RowCount == nil {
		return 0
	}
	return *res.RowCount
}
