// SPDX-License-Identifier: AGPL-3.0-or-later

package opaengine

import (
	"testing"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// A request without a statement names its action, for example the check
// whether a subject may read a table. A module sees it as input.action.
func TestBuildInput_ActionWithoutStatement(t *testing.T) {
	if got := buildInput(policy.Input{Action: apitypes.ActionSelect})["action"]; got != "SELECT" {
		t.Errorf("input.action = %q, want SELECT", got)
	}
	if got := buildInput(policy.Input{})["action"]; got != "" {
		t.Errorf("input.action = %q, want an empty text", got)
	}
}

// A second entry for the same table or column would replace the first one,
// so the decision is refused instead.
func TestDecodeDecision_DuplicateFilterOrMaskFails(t *testing.T) {
	tables := []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "t"}}
	pred := `{"column": "x", "op": "Equals", "value": "1"}`
	cases := map[string]string{
		"row_filters":  `{"allow": true, "row_filters": [{"table": "pg.public.t", "predicate": ` + pred + `}, {"table": "pg.public.t", "predicate": ` + pred + `}]}`,
		"column_masks": `{"allow": true, "column_masks": [{"table": "pg.public.t", "column": "c", "mask": {"type": "null"}}, {"table": "pg.public.t", "column": "c", "mask": {"type": "null"}}]}`,
	}
	for name, raw := range cases {
		if _, err := decodeDecision([]byte(raw), tables); err == nil {
			t.Errorf("%s: duplicate entry was accepted", name)
		}
	}
}
