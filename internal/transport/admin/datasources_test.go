// SPDX-License-Identifier: AGPL-3.0-or-later

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bino-bi/sluice/internal/datasource"
	"github.com/bino-bi/sluice/internal/transport/admin"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgds "github.com/bino-bi/sluice/pkg/datasource"
	"github.com/bino-bi/sluice/pkg/datasource/testfakes"
)

const (
	plainType = "fake_plain_admin_test"
	stateType = "fake_state_admin_test"
)

type stateSource struct{ pkgds.DataSource }

func (stateSource) State() string { return "not_built" }

func init() {
	pkgds.Register(plainType, func(_ context.Context, spec pkgds.Spec) (pkgds.DataSource, error) {
		return testfakes.New(spec.Name, pkgds.Schema{}), nil
	})
	pkgds.Register(stateType, func(_ context.Context, spec pkgds.Spec) (pkgds.DataSource, error) {
		return stateSource{testfakes.New(spec.Name, pkgds.Schema{})}, nil
	})
}

func TestHandleDataSources_Status(t *testing.T) {
	t.Parallel()
	ds := func(name, typ string) *apitypes.DataSource {
		return &apitypes.DataSource{
			Metadata: apitypes.ObjectMeta{Name: name},
			Spec:     apitypes.DataSourceSpec{Type: apitypes.DataSourceType(typ)},
		}
	}
	reg, err := datasource.New(context.Background(), datasource.Options{
		Snapshot:       &datasource.Snapshot{DataSources: []*apitypes.DataSource{ds("pg", plainType), ds("syn", stateType)}},
		HealthInterval: -1,
	})
	if err != nil {
		t.Fatalf("datasource.New: %v", err)
	}
	t.Cleanup(func() { _ = reg.Close() })

	srv := admin.New(admin.Config{Listen: ":0", Enabled: true}, admin.Deps{Sources: reg})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/datasources", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", w.Code)
	}
	var body struct {
		DataSources []map[string]any `json:"datasources"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.DataSources) != 2 {
		t.Fatalf("datasources = %v", body.DataSources)
	}
	if _, ok := body.DataSources[0]["status"]; ok {
		t.Errorf("pg has a status field: %v", body.DataSources[0])
	}
	if got := body.DataSources[1]["status"]; got != "not_built" {
		t.Errorf("syn status = %v; want not_built", got)
	}
}
