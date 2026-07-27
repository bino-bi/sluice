// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import (
	"testing"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

func TestInferFormat(t *testing.T) {
	cases := []struct {
		in   string
		want apitypes.FileFormat
		err  bool
	}{
		{"s3://b/data/file.parquet", apitypes.FileFormatParquet, false},
		{"s3://b/data/*.parquet", apitypes.FileFormatParquet, false},
		{"s3://b/reports/2026.CSV", apitypes.FileFormatCSV, false},
		{"https://host/api/export.json", apitypes.FileFormatJSON, false},
		{"https://host/logs/events.ndjson", apitypes.FileFormatJSON, false},
		{"https://host/logs/events.jsonl", apitypes.FileFormatJSON, false},
		{"https://host/sheet.xlsx", apitypes.FileFormatXLSX, false},
		{"https://host/sheet.xlsx?X-Amz-Signature=abc", apitypes.FileFormatXLSX, false},
		{"bare/path/file.csv", apitypes.FileFormatCSV, false},
		{"s3://b/data/file", "", true},
		{"s3://b/data/file.txt", "", true},
	}
	for _, tc := range cases {
		got, err := InferFormat(tc.in)
		if tc.err {
			if err == nil {
				t.Errorf("InferFormat(%q): expected error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("InferFormat(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("InferFormat(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestReaderSQL(t *testing.T) {
	got, err := ReaderSQL(apitypes.FileFormatCSV, "s3://b/it's.csv")
	if err != nil {
		t.Fatalf("ReaderSQL: %v", err)
	}
	want := "read_csv('s3://b/it''s.csv')"
	if got != want {
		t.Fatalf("ReaderSQL = %q; want %q", got, want)
	}

	for f, fn := range map[apitypes.FileFormat]string{
		apitypes.FileFormatParquet: "read_parquet('p')",
		apitypes.FileFormatJSON:    "read_json_auto('p')",
		apitypes.FileFormatXLSX:    "read_xlsx('p')",
	} {
		got, err := ReaderSQL(f, "p")
		if err != nil {
			t.Errorf("ReaderSQL(%q): %v", f, err)
		} else if got != fn {
			t.Errorf("ReaderSQL(%q) = %q; want %q", f, got, fn)
		}
	}

	if _, err := ReaderSQL("avro", "p"); err == nil {
		t.Error("ReaderSQL(avro): expected error")
	}
}
