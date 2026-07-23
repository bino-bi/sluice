// SPDX-License-Identifier: AGPL-3.0-or-later

package executor_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/bino-bi/sluice/internal/datasource"
	_ "github.com/bino-bi/sluice/internal/datasource/drivers/sqlitefile"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// TestHealthSweepThroughSQLiteAttach proves the real probe path: the
// registry borrows a connection from the executor pool via NewSQLPool,
// the sqlitefile driver's HealthCheck runs against the ATTACH'd catalog,
// and Status.Healthy flips from its fail-closed false to true.
func TestHealthSweepThroughSQLiteAttach(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "health.sqlite")
	createSQLiteFixture(t, dbPath)

	ds := &apitypes.DataSource{
		TypeMeta: apitypes.TypeMeta{APIVersion: "sluice.io/v1alpha1", Kind: apitypes.KindDataSource},
		Metadata: apitypes.ObjectMeta{Name: "fixture"},
		Spec: apitypes.DataSourceSpec{
			Type:     apitypes.DSSQLite,
			Path:     dbPath,
			Readonly: true,
		},
	}

	reg, err := datasource.New(context.Background(), datasource.Options{
		Snapshot:       &datasource.Snapshot{DataSources: []*apitypes.DataSource{ds}},
		HealthInterval: -1, // only SetPool's one-shot sweep runs
		FailFast:       true,
	})
	if err != nil {
		t.Fatalf("datasource.New: %v", err)
	}
	t.Cleanup(func() { _ = reg.Close() })

	e, err := executor.New(context.Background(), executor.Config{
		AttachHook: reg.AttachHook(),
	})
	if err != nil {
		t.Fatalf("executor.New: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })

	if reg.Statuses()[0].Healthy {
		t.Fatal("Healthy must start false before any probe")
	}

	reg.SetPool(context.Background(), datasource.NewSQLPool(e.DB()))

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if reg.Statuses()[0].Healthy {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	s := reg.Statuses()[0]
	if !s.Healthy {
		t.Fatalf("sweep did not flip Healthy: %+v", s)
	}
	if s.LastCheck.IsZero() || s.LastError != "" {
		t.Fatalf("unexpected status after healthy probe: %+v", s)
	}
}
