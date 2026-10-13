// SPDX-License-Identifier: AGPL-3.0-or-later

package executor_test

// End-to-end coverage for the http driver: a real datasource.Registry
// instantiates it from an apitypes manifest, the executor attaches and
// hardens with the URL-derived allowed_paths carve-out, and hardened
// SELECTs flow through one view per format. The stub server records the
// Authorization header so the per-origin EXTRA_HTTP_HEADERS secret is
// verified to actually reach the wire.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/bino-bi/sluice/internal/datasource"
	_ "github.com/bino-bi/sluice/internal/datasource/drivers/httpfile"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

func TestHTTPDriverAllFormatsAndHeadersThroughHardenedExecutor(t *testing.T) {
	ensureHTTPFS(t)
	dir := t.TempDir()
	genFixtures(t, filepath.Join(dir, "bucket")) // reuse the four-format fixture set

	var (
		mu      sync.Mutex
		auths   = map[string]bool{}
		fileSrv = http.FileServer(http.Dir(dir))
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths[r.Header.Get("Authorization")] = true
		mu.Unlock()
		fileSrv.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	ds := &apitypes.DataSource{
		TypeMeta: apitypes.TypeMeta{APIVersion: "sluice.bino.bi/v1alpha1", Kind: apitypes.KindDataSource},
		Metadata: apitypes.ObjectMeta{Name: "web"},
		Spec: apitypes.DataSourceSpec{
			Type: apitypes.DSHTTP,
			URLs: []string{
				srv.URL + "/bucket/nested/facts.parquet",
				srv.URL + "/bucket/countries.csv",
				srv.URL + "/bucket/export.json",
				srv.URL + "/bucket/plan.xlsx",
			},
			HeadersRef: "secret://test/headers",
		},
	}

	reg, err := datasource.New(context.Background(), datasource.Options{
		Snapshot:       &datasource.Snapshot{DataSources: []*apitypes.DataSource{ds}},
		Secrets:        staticSecrets{payload: []byte(`{"Authorization":"Bearer test-token"}`)},
		HealthInterval: -1,
		FailFast:       true,
	})
	if err != nil {
		t.Fatalf("datasource.New: %v", err)
	}
	t.Cleanup(func() { _ = reg.Close() })

	e, err := executor.New(context.Background(), executor.Config{
		Harden:     executor.HardenConfig{AllowedExternalPaths: reg.ExternalPaths()},
		AttachHook: reg.AttachHook(),
	})
	if err != nil {
		t.Fatalf("executor.New: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })

	cases := []struct {
		view string
		want int64
	}{
		{"bucket_nested_facts_parquet", 2},
		{"bucket_countries_csv", 2},
		{"bucket_export_json", 3},
		{"bucket_plan_xlsx", 2},
	}
	for _, tc := range cases {
		n, err := countQuery(t, e, "SELECT count(*) FROM web.main."+tc.view)
		if err != nil {
			t.Errorf("%s: %v", tc.view, err)
			continue
		}
		if n != tc.want {
			t.Errorf("%s: count = %d; want %d", tc.view, n, tc.want)
		}
	}

	// The configured headers must have reached the wire.
	mu.Lock()
	gotBearer := auths["Bearer test-token"]
	mu.Unlock()
	if !gotBearer {
		t.Error("Authorization header never arrived at the server")
	}

	// A file on the same origin that is NOT in urls has no view and no
	// carve-out: reading it directly must fail.
	if _, err := countQuery(t, e, "SELECT count(*) FROM read_csv('"+srv.URL+"/bucket/other.csv')"); err == nil {
		t.Error("read of a non-listed URL succeeded")
	}
}
