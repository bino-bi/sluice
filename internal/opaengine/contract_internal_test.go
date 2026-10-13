// SPDX-License-Identifier: AGPL-3.0-or-later

package opaengine

import (
	"testing"

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
