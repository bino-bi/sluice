// SPDX-License-Identifier: AGPL-3.0-or-later

package budget_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/budget"
)

// A subject budget of one issuer does not apply to the same sub of another
// issuer. A subject budget without an issuer applies to every issuer.
func TestBudget_SubjectSpecIsIssuerAware(t *testing.T) {
	m := newManager(t, filepath.Join(t.TempDir(), "b.db"))
	defer func() { _ = m.Close(context.Background()) }()
	m.SetSpecs(map[budget.SubjectKey]budget.Spec{
		{Subject: "alice", Issuer: "keys"}: {RowsPerDay: 1},
		{Subject: "bob"}:                   {RowsPerDay: 1},
	}, nil)
	ctx := context.Background()
	for _, iss := range []string{"keys", "idp"} {
		m.Record("alice", iss, time.Millisecond, 5)
		m.Record("bob", iss, time.Millisecond, 5)
	}
	if m.Check(ctx, "alice", "keys") == nil {
		t.Error("alice of issuer keys: want over budget")
	}
	if err := m.Check(ctx, "alice", "idp"); err != nil {
		t.Errorf("alice of another issuer got the budget of issuer keys: %v", err)
	}
	if m.Check(ctx, "bob", "idp") == nil {
		t.Error("bob without issuer in the spec: want over budget for every issuer")
	}
}
