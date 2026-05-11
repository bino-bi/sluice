// SPDX-License-Identifier: AGPL-3.0-or-later

package s3parquet

import (
	"context"
	"testing"

	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

func TestNewDriverRequiresBucket(t *testing.T) {
	_, err := newDriver(context.Background(), pkgds.Spec{
		Name: "s3", Type: Type, Raw: map[string]any{"region": "eu-west-1"},
	})
	if err == nil {
		t.Fatal("expected error when bucket is missing")
	}
}

func TestNewDriverRequiresRegion(t *testing.T) {
	_, err := newDriver(context.Background(), pkgds.Spec{
		Name: "s3", Type: Type, Raw: map[string]any{"bucket": "orders"},
	})
	if err == nil {
		t.Fatal("expected error when region is missing")
	}
}

func TestNewDriverNormalisesAllowedPaths(t *testing.T) {
	ds, err := newDriver(context.Background(), pkgds.Spec{
		Name: "s3", Type: Type,
		Raw: map[string]any{
			"bucket":       "orders",
			"region":       "eu-west-1",
			"allowedPaths": []any{"s3://orders/raw/**", "s3://orders//double//slash"},
		},
	})
	if err != nil {
		t.Fatalf("newDriver: %v", err)
	}
	d := ds.(*driver)
	if len(d.allowedPaths) != 2 {
		t.Fatalf("allowedPaths = %v", d.allowedPaths)
	}
	if d.allowedPaths[1] != "s3://orders/double/slash" {
		t.Errorf("path not normalised: %q", d.allowedPaths[1])
	}
	if !d.Readonly() {
		t.Error("must be read-only")
	}
}

func TestPathAllowed(t *testing.T) {
	ds, err := newDriver(context.Background(), pkgds.Spec{
		Name: "s3", Type: Type,
		Raw: map[string]any{
			"bucket":       "orders",
			"region":       "eu-west-1",
			"allowedPaths": []any{"s3://orders/raw/**", "s3://orders/ref/*.parquet"},
		},
	})
	if err != nil {
		t.Fatalf("newDriver: %v", err)
	}
	d := ds.(*driver)
	if !d.PathAllowed("s3://orders/raw/2026/04/a.parquet") {
		t.Error("raw glob should allow nested paths")
	}
	if !d.PathAllowed("s3://orders/ref/dim.parquet") {
		t.Error("ref *.parquet should match")
	}
	if d.PathAllowed("s3://orders/staging/x") {
		t.Error("staging must be rejected")
	}
	if d.PathAllowed("") {
		t.Error("empty path must be rejected")
	}
}

func TestTrimBucketPrefix(t *testing.T) {
	cases := map[string]string{
		"s3://orders/raw/**":          "raw/**",
		"s3://orders/ref/dim.parquet": "ref/dim.parquet",
		"s3://bucket":                 "bucket",
		"plain-path":                  "plain-path",
	}
	for in, want := range cases {
		if got := trimBucketPrefix(in); got != want {
			t.Errorf("trimBucketPrefix(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestHttpsOrFalse(t *testing.T) {
	if got := httpsOrFalse("http://minio:9000"); got != "false" {
		t.Errorf("http endpoint = %q; want false", got)
	}
	if got := httpsOrFalse("https://s3.us-east-1.amazonaws.com"); got != "true" {
		t.Errorf("https endpoint = %q; want true", got)
	}
}

func TestFactoryRegisteredViaInit(t *testing.T) {
	if _, ok := pkgds.Lookup(Type); !ok {
		t.Fatal("s3parquet driver did not self-register via init()")
	}
}
