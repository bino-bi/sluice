// SPDX-License-Identifier: AGPL-3.0-or-later

package s3

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/bino-bi/sluice/pkg/apitypes"
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
			"format":       "parquet",
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

func TestNewDriverPutsBarePathsUnderTheBucket(t *testing.T) {
	ds, err := newDriver(context.Background(), pkgds.Spec{
		Name: "s3", Type: Type,
		Raw: map[string]any{
			"bucket":       "orders",
			"region":       "eu-west-1",
			"allowedPaths": []any{"sales/*.parquet", "/ref/dim.csv"},
		},
	})
	if err != nil {
		t.Fatalf("newDriver: %v", err)
	}
	d := ds.(*driver)
	want := []string{"s3://orders/sales/*.parquet", "s3://orders/ref/dim.csv"}
	for i, w := range want {
		if d.allowedPaths[i] != w || d.views[i].path != w {
			t.Errorf("path %d = %q (view %q), want %q", i, d.allowedPaths[i], d.views[i].path, w)
		}
	}
}

func TestNewDriverRefusesPathsOutsideTheBucket(t *testing.T) {
	for _, p := range []string{"s3://other/x.parquet", "s3://orders-b/x.parquet", "s3://orders", "https://orders/x.parquet", " ", "s3://orders/../other/x.parquet", "a/../../other/x.parquet", "s3://orders/./x.parquet"} {
		_, err := newDriver(context.Background(), pkgds.Spec{
			Name: "s3", Type: Type,
			Raw: map[string]any{
				"bucket": "orders", "region": "eu-west-1", "format": "parquet",
				"allowedPaths": []any{p},
			},
		})
		if err == nil {
			t.Errorf("allowedPaths %q: accepted", p)
		}
	}
}

func TestResolveViewsInfersPerPath(t *testing.T) {
	ds, err := newDriver(context.Background(), pkgds.Spec{
		Name: "s3", Type: Type,
		Raw: map[string]any{
			"bucket": "orders",
			"region": "eu-west-1",
			"allowedPaths": []any{
				"s3://orders/facts/*.parquet",
				"s3://orders/dims/countries.csv",
				"s3://orders/api/export.json",
				"s3://orders/manual/plan.xlsx",
			},
		},
	})
	if err != nil {
		t.Fatalf("newDriver: %v", err)
	}
	d := ds.(*driver)
	want := []apitypes.FileFormat{
		apitypes.FileFormatParquet,
		apitypes.FileFormatCSV,
		apitypes.FileFormatJSON,
		apitypes.FileFormatXLSX,
	}
	if len(d.views) != len(want) {
		t.Fatalf("views = %+v; want %d entries", d.views, len(want))
	}
	for i, f := range want {
		if d.views[i].format != f {
			t.Errorf("views[%d].format = %q; want %q", i, d.views[i].format, f)
		}
	}
	if !d.needsExcel {
		t.Error("needsExcel = false; want true with an xlsx path")
	}
}

func TestResolveViewsExplicitOverride(t *testing.T) {
	ds, err := newDriver(context.Background(), pkgds.Spec{
		Name: "s3", Type: Type,
		Raw: map[string]any{
			"bucket":       "orders",
			"region":       "eu-west-1",
			"format":       "csv",
			"allowedPaths": []any{"s3://orders/exports/*.dat"},
		},
	})
	if err != nil {
		t.Fatalf("newDriver: %v", err)
	}
	d := ds.(*driver)
	if d.views[0].format != apitypes.FileFormatCSV {
		t.Errorf("format = %q; want csv override", d.views[0].format)
	}
	if d.needsExcel {
		t.Error("needsExcel = true; want false without xlsx")
	}
}

func TestResolveViewsUndeterminableFormatFails(t *testing.T) {
	_, err := newDriver(context.Background(), pkgds.Spec{
		Name: "s3", Type: Type,
		Raw: map[string]any{
			"bucket":       "orders",
			"region":       "eu-west-1",
			"allowedPaths": []any{"s3://orders/raw/**"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "set spec.format") {
		t.Fatalf("err = %v; want format-inference error", err)
	}
}

func TestResolveViewsRejectsXLSXGlob(t *testing.T) {
	_, err := newDriver(context.Background(), pkgds.Spec{
		Name: "s3", Type: Type,
		Raw: map[string]any{
			"bucket":       "orders",
			"region":       "eu-west-1",
			"allowedPaths": []any{"s3://orders/sheets/*.xlsx"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "glob") {
		t.Fatalf("err = %v; want xlsx-glob rejection", err)
	}
}

func TestResolveViewsRejectsNameCollision(t *testing.T) {
	_, err := newDriver(context.Background(), pkgds.Spec{
		Name: "s3", Type: Type,
		Raw: map[string]any{
			"bucket":       "orders",
			"region":       "eu-west-1",
			"allowedPaths": []any{"s3://orders/a/data.csv", "s3://orders/a_data.csv"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("err = %v; want view-name collision error", err)
	}
}

func TestExternalPaths(t *testing.T) {
	ds, err := newDriver(context.Background(), pkgds.Spec{
		Name: "s3", Type: Type,
		Raw: map[string]any{"bucket": "orders", "region": "eu-west-1"},
	})
	if err != nil {
		t.Fatalf("newDriver: %v", err)
	}
	ep, ok := ds.(pkgds.ExternalPather)
	if !ok {
		t.Fatal("driver does not implement ExternalPather")
	}
	got := ep.ExternalPaths()
	if len(got) != 1 || got[0] != "s3://orders/" {
		t.Fatalf("ExternalPaths = %v; want [s3://orders/]", got)
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
		t.Fatal("s3 driver did not self-register via init()")
	}
}

type mapResolver map[string]string

func (m mapResolver) Resolve(_ context.Context, ref string) ([]byte, error) {
	v, ok := m[ref]
	if !ok {
		return nil, errors.New("no secret " + ref)
	}
	return []byte(v), nil
}

// TestSecretsAreScopedToTheBucket attaches two s3 sources with other keys
// to one DuckDB instance and asks DuckDB which secret each path uses.
func TestSecretsAreScopedToTheBucket(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.ExecContext(ctx, "LOAD httpfs"); err != nil {
		t.Skipf("httpfs not available offline: %v", err)
	}

	res := mapResolver{
		"secret://env/A": `{"key_id":"ka","secret":"sa"}`,
		"secret://env/B": `{"key_id":"kb","secret":"sb"}`,
	}
	for _, src := range []struct{ name, bucket, ref string }{
		{"src_a", "sales", "secret://env/A"},
		{"src_b", "sales-eu", "secret://env/B"},
	} {
		ds, err := newDriver(ctx, pkgds.Spec{Name: src.name, Type: Type, Raw: map[string]any{
			"bucket": src.bucket, "region": "eu-west-1", "credentialsRef": src.ref,
		}})
		if err != nil {
			t.Fatalf("newDriver: %v", err)
		}
		if err := ds.Attach(ctx, conn, pkgds.AttachOptions{CatalogName: src.name, SecretResolver: res}); err != nil {
			t.Fatalf("Attach %s: %v", src.name, err)
		}
	}

	for path, want := range map[string]string{
		"s3://sales/x.parquet":     "sluice_s3_src_a",
		"s3://sales-eu/x.parquet":  "sluice_s3_src_b",
		"s3://sales-eu2/x.parquet": "",
		"s3://other/x.parquet":     "",
	} {
		var got string
		err := conn.QueryRowContext(ctx, "SELECT name FROM which_secret(?, 's3')", path).Scan(&got)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("which_secret(%s): %v", path, err)
		}
		if got != want {
			t.Errorf("which_secret(%s) = %q, want %q", path, got, want)
		}
	}
}
