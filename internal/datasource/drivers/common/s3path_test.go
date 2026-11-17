// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import "testing"

func TestNormalizeS3URI(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{"", "", false},
		{"s3://bucket/prefix", "s3://bucket/prefix", false},
		{"  s3://bucket/a  ", "s3://bucket/a", false},
		{"s3://bucket//a///b", "s3://bucket/a/b", false},
		{"https://bucket.s3", "", true},
		{"bucket/path", "", true},
	}
	for _, c := range cases {
		got, err := NormalizeS3URI(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("NormalizeS3URI(%q) err=%v; wantErr=%v", c.in, err, c.wantErr)
		}
		if got != c.want {
			t.Errorf("NormalizeS3URI(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeViewName(t *testing.T) {
	cases := map[string]string{
		"orders.parquet": "orders_parquet",
		"dim_customer":   "dim_customer",
		"123leading":     "_23leading",
		"":               "v_",
		"s3://a/b":       "s3___a_b",
		"has-dashes":     "has_dashes",
		"with spaces.pq": "with_spaces_pq",
	}
	for in, want := range cases {
		if got := SanitizeViewName(in); got != want {
			t.Errorf("SanitizeViewName(%q) = %q; want %q", in, got, want)
		}
	}
}
