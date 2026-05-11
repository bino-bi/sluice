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

func TestMatchAllowed(t *testing.T) {
	allowed := []string{
		"s3://orders/raw/**",
		"s3://public/reference/*.parquet",
		"warehouse/dim_*.parquet",
	}

	good := []string{
		"s3://orders/raw/2026/04/orders.parquet",
		"s3://orders/raw/x.parquet",
		"s3://public/reference/zipcodes.parquet",
		"s3://warehouse/dim_customer.parquet",
	}
	bad := []string{
		"",
		"s3://orders/processed/a.parquet",
		"s3://public/reference/subdir/x.parquet", // * matches one segment only
		"s3://other/raw/a.parquet",
		"s3://warehouse/fact_orders.parquet",
	}
	for _, p := range good {
		if !MatchAllowed(allowed, p) {
			t.Errorf("MatchAllowed(%q) = false; want true", p)
		}
	}
	for _, p := range bad {
		if MatchAllowed(allowed, p) {
			t.Errorf("MatchAllowed(%q) = true; want false", p)
		}
	}

	// Empty whitelist rejects everything.
	if MatchAllowed(nil, "s3://orders/raw/x") {
		t.Error("empty allowedPaths must reject")
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
