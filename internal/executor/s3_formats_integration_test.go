// SPDX-License-Identifier: AGPL-3.0-or-later

package executor_test

// End-to-end coverage for the multi-format s3 driver: a real
// datasource.Registry instantiates the driver from an apitypes manifest,
// the executor attaches and hardens (with the registry-derived
// allowed_directories carve-out), and hardened SELECTs flow through one
// view per format. An httptest file server plays the S3 endpoint
// (path-style); parquet and xlsx fixtures are generated with a raw,
// unhardened DuckDB connection at setup.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	duckdb "github.com/marcboeker/go-duckdb/v2"

	"github.com/bino-bi/sluice/internal/datasource"
	_ "github.com/bino-bi/sluice/internal/datasource/drivers/s3"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// staticSecrets resolves every ref to the same payload.
type staticSecrets struct{ payload []byte }

func (s staticSecrets) Resolve(_ context.Context, _ string) ([]byte, error) {
	return s.payload, nil
}

// genFixtures writes one file per format under dir/bucket using a raw
// DuckDB connection (parquet is built in; xlsx needs the excel
// extension — the caller has already ensured it installs).
func genFixtures(t *testing.T, bucketDir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(bucketDir, "nested"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeCSV(t, filepath.Join(bucketDir, "countries.csv"), "code,name\nDE,Germany\nFR,France\n")
	writeCSV(t, filepath.Join(bucketDir, "export.json"), `[{"id":1,"tag":"a"},{"id":2,"tag":"b"},{"id":3,"tag":"c"}]`)

	connector, err := duckdb.NewConnector("", nil)
	if err != nil {
		t.Fatalf("NewConnector: %v", err)
	}
	db := sql.OpenDB(connector)
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	stmts := []string{
		fmt.Sprintf(`COPY (SELECT * FROM (VALUES (1, 'alpha'), (2, 'bravo')) t(id, name)) TO '%s' (FORMAT parquet)`,
			filepath.Join(bucketDir, "nested", "facts.parquet")),
		"INSTALL excel",
		"LOAD excel",
		fmt.Sprintf(`COPY (SELECT * FROM (VALUES (10, 'x'), (20, 'y')) t(num, label)) TO '%s' (FORMAT xlsx, HEADER true)`,
			filepath.Join(bucketDir, "plan.xlsx")),
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			if strings.Contains(s, "INSTALL") {
				t.Skipf("INSTALL excel failed (offline?): %v", err)
			}
			t.Fatalf("fixture %q: %v", s, err)
		}
	}
}

func TestS3DriverAllFormatsThroughHardenedExecutor(t *testing.T) {
	ensureHTTPFS(t)
	dir := t.TempDir()
	genFixtures(t, filepath.Join(dir, "bucket"))
	srv := serveDir(t, dir)

	ds := &apitypes.DataSource{
		TypeMeta: apitypes.TypeMeta{APIVersion: "sluice.bino.bi/v1alpha1", Kind: apitypes.KindDataSource},
		Metadata: apitypes.ObjectMeta{Name: "files"},
		Spec: apitypes.DataSourceSpec{
			Type:           apitypes.DSS3,
			Bucket:         "bucket",
			Region:         "us-east-1",
			Endpoint:       srv.URL, // http://127.0.0.1:port — scheme decides USE_SSL
			CredentialsRef: "secret://test/creds",
			AllowedPaths: []string{
				"s3://bucket/nested/facts.parquet",
				"s3://bucket/countries.csv",
				"s3://bucket/export.json",
				"s3://bucket/plan.xlsx",
			},
			Readonly: true,
		},
	}

	reg, err := datasource.New(context.Background(), datasource.Options{
		Snapshot:       &datasource.Snapshot{DataSources: []*apitypes.DataSource{ds}},
		Secrets:        staticSecrets{payload: []byte(`{"key_id":"k","secret":"s"}`)},
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
		{"nested_facts_parquet", 2},
		{"countries_csv", 2},
		{"export_json", 3},
		{"plan_xlsx", 2},
	}
	for _, tc := range cases {
		n, err := countQuery(t, e, "SELECT count(*) FROM files.main."+tc.view)
		if err != nil {
			t.Errorf("%s: %v", tc.view, err)
			continue
		}
		if n != tc.want {
			t.Errorf("%s: count = %d; want %d", tc.view, n, tc.want)
		}
	}

	// The registry-derived carve-out covers only the bucket: other
	// buckets on the same endpoint stay unreachable.
	if _, err := countQuery(t, e, "SELECT count(*) FROM read_csv('s3://elsewhere/x.csv')"); err == nil {
		t.Error("read outside the bucket carve-out succeeded")
	}
}
