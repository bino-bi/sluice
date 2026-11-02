// SPDX-License-Identifier: AGPL-3.0-or-later

package datasource

import (
	"testing"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

func TestMarshalSpecSynthetic(t *testing.T) {
	spec := &apitypes.SyntheticSpec{Source: "crm"}
	raw := marshalSpec(apitypes.DataSourceSpec{Type: apitypes.DSSynthetic, Synthetic: spec}, "/data/syn")
	if raw["synthetic"] != spec {
		t.Errorf("synthetic = %v; want the typed spec", raw["synthetic"])
	}
	if raw["syntheticDir"] != "/data/syn" {
		t.Errorf("syntheticDir = %v", raw["syntheticDir"])
	}

	other := marshalSpec(apitypes.DataSourceSpec{Type: apitypes.DSDuckDBFile, Path: "x.duckdb"}, "/data/syn")
	if _, ok := other["syntheticDir"]; ok {
		t.Error("syntheticDir set for another type")
	}
	if _, ok := other["synthetic"]; ok {
		t.Error("synthetic set for another type")
	}
}
