// SPDX-License-Identifier: AGPL-3.0-or-later

package httpfile

import (
	"context"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/datasource/drivers/common"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

func mk(t *testing.T, raw map[string]any) *driver {
	t.Helper()
	ds, err := newDriver(context.Background(), pkgds.Spec{Name: "web", Type: Type, Raw: raw})
	if err != nil {
		t.Fatalf("newDriver: %v", err)
	}
	return ds.(*driver)
}

func TestNewDriverRequiresURLs(t *testing.T) {
	_, err := newDriver(context.Background(), pkgds.Spec{Name: "web", Type: Type, Raw: map[string]any{}})
	if err == nil {
		t.Fatal("expected error when urls is missing")
	}
}

func TestNewDriverRejectsBadURLs(t *testing.T) {
	for _, bad := range []string{
		"ftp://host/x.csv",
		"/relative/x.csv",
		"https:///nohost.csv",
		"https://host/",              // no file path
		"https://host/data/*.csv",    // glob
		"https://host/data/[ab].csv", // glob
	} {
		_, err := newDriver(context.Background(), pkgds.Spec{
			Name: "web", Type: Type, Raw: map[string]any{"urls": []any{bad}},
		})
		if err == nil {
			t.Errorf("url %q: expected error", bad)
		}
	}
}

func TestResolveViewsInfersPerURL(t *testing.T) {
	d := mk(t, map[string]any{"urls": []any{
		"https://host/data/report.csv",
		"https://host/api/export.json",
		"https://other:8443/facts.parquet",
		"https://host/sheet.xlsx?X-Sig=abc",
	}})
	want := []apitypes.FileFormat{
		apitypes.FileFormatCSV,
		apitypes.FileFormatJSON,
		apitypes.FileFormatParquet,
		apitypes.FileFormatXLSX,
	}
	for i, f := range want {
		if d.views[i].format != f {
			t.Errorf("views[%d].format = %q; want %q", i, d.views[i].format, f)
		}
	}
	if !d.needsExcel {
		t.Error("needsExcel = false; want true with an xlsx URL")
	}
	wantOrigins := []string{"https://host", "https://other:8443"}
	if len(d.origins) != 2 || d.origins[0] != wantOrigins[0] || d.origins[1] != wantOrigins[1] {
		t.Errorf("origins = %v; want %v", d.origins, wantOrigins)
	}
}

func TestResolveViewsExplicitOverride(t *testing.T) {
	d := mk(t, map[string]any{
		"urls":   []any{"https://host/export.dat"},
		"format": "json",
	})
	if d.views[0].format != apitypes.FileFormatJSON {
		t.Errorf("format = %q; want json override", d.views[0].format)
	}
}

func TestResolveViewsUndeterminableFormatFails(t *testing.T) {
	_, err := newDriver(context.Background(), pkgds.Spec{
		Name: "web", Type: Type,
		Raw: map[string]any{"urls": []any{"https://host/export"}},
	})
	if err == nil || !strings.Contains(err.Error(), "set spec.format") {
		t.Fatalf("err = %v; want format-inference error", err)
	}
}

func TestResolveViewsRejectsNameCollision(t *testing.T) {
	_, err := newDriver(context.Background(), pkgds.Spec{
		Name: "web", Type: Type,
		Raw: map[string]any{"urls": []any{
			"https://a/data.csv",
			"https://b/data.csv", // same path → same sanitized view name
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("err = %v; want view-name collision error", err)
	}
}

func TestExternalPathsIsURLList(t *testing.T) {
	urls := []any{"https://host/a.csv", "https://host/b.json"}
	d := mk(t, map[string]any{"urls": urls})
	ep, ok := any(d).(pkgds.ExternalPather)
	if !ok {
		t.Fatal("driver does not implement ExternalPather")
	}
	got := ep.ExternalPaths()
	if len(got) != 2 || got[0] != "https://host/a.csv" || got[1] != "https://host/b.json" {
		t.Fatalf("ExternalPaths = %v; want the exact URL list", got)
	}
}

func TestReadonlyAlwaysTrue(t *testing.T) {
	d := mk(t, map[string]any{"urls": []any{"https://host/a.csv"}})
	if !d.Readonly() {
		t.Error("http driver must always be read-only")
	}
}

func TestHeaderSecretRendering(t *testing.T) {
	stmt, err := common.BuildCreateSecret("sluice_http_web_0", secretTypeHTTP, []common.SecretArg{
		{Key: "EXTRA_HTTP_HEADERS", Value: common.MapLiteral(map[string]string{
			"Authorization": "Bearer tok'en",
			"X-Api-Key":     "k",
		}), Raw: true},
		{Key: "SCOPE", Value: "https://host"},
	})
	if err != nil {
		t.Fatalf("BuildCreateSecret: %v", err)
	}
	if !strings.Contains(stmt, "EXTRA_HTTP_HEADERS MAP {'Authorization': 'Bearer tok''en', 'X-Api-Key': 'k'}") {
		t.Errorf("headers not rendered as sorted MAP literal:\n%s", stmt)
	}
	if !strings.Contains(stmt, "SCOPE 'https://host'") {
		t.Errorf("scope missing:\n%s", stmt)
	}
}

func TestFactoryRegisteredViaInit(t *testing.T) {
	if _, ok := pkgds.Lookup(Type); !ok {
		t.Fatal("http driver did not self-register via init()")
	}
}
