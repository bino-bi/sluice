// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// InferFormat derives the file format for a path or URL from its
// extension. URLs are parsed first so query strings (pre-signed URLs)
// do not defeat the extension sniff; glob patterns keep their extension
// ("data/*.parquet" infers parquet).
func InferFormat(pathOrURL string) (apitypes.FileFormat, error) {
	p := pathOrURL
	if u, err := url.Parse(pathOrURL); err == nil && u.Path != "" {
		p = u.Path
	}
	switch strings.ToLower(path.Ext(p)) {
	case ".parquet":
		return apitypes.FileFormatParquet, nil
	case ".csv":
		return apitypes.FileFormatCSV, nil
	case ".json", ".ndjson", ".jsonl":
		return apitypes.FileFormatJSON, nil
	case ".xlsx":
		return apitypes.FileFormatXLSX, nil
	}
	return "", fmt.Errorf("cannot infer file format of %q; set spec.format", pathOrURL)
}

// ReaderSQL renders the DuckDB table-function call that reads p as
// format. p is escaped for embedding in a single-quoted SQL literal.
func ReaderSQL(format apitypes.FileFormat, p string) (string, error) {
	escaped := EscapeSQLString(p)
	switch format {
	case apitypes.FileFormatParquet:
		return fmt.Sprintf("read_parquet('%s')", escaped), nil
	case apitypes.FileFormatCSV:
		return fmt.Sprintf("read_csv('%s')", escaped), nil
	case apitypes.FileFormatJSON:
		return fmt.Sprintf("read_json_auto('%s')", escaped), nil
	case apitypes.FileFormatXLSX:
		return fmt.Sprintf("read_xlsx('%s')", escaped), nil
	}
	return "", fmt.Errorf("unknown file format %q", format)
}
