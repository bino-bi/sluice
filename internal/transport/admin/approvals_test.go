// SPDX-License-Identifier: AGPL-3.0-or-later

package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/approval"
	"github.com/bino-bi/sluice/internal/transport/admin"
)

type fakePendingLister struct{ views []approval.View }

func (f fakePendingLister) Pending() []approval.View { return f.views }

func TestHandleApprovals_ListsPendingNoTokens(t *testing.T) {
	t.Parallel()
	lister := fakePendingLister{views: []approval.View{
		{ID: "apr_1", State: approval.StatePending, Subject: approval.Subject{ID: "alice"}, SQLHash: "abc"},
	}}
	srv := admin.New(admin.Config{Listen: ":0", Enabled: true}, admin.Deps{Approvals: lister})
	r := httptest.NewRequest(http.MethodGet, "/admin/approvals", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct {
		Count   int              `json:"count"`
		Pending []map[string]any `json:"pending"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Count != 1 || body.Pending[0]["approval_id"] != "apr_1" {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
	// No token field ever leaks (View is token-free).
	if strings.Contains(w.Body.String(), "token") {
		t.Errorf("response contains a token field: %s", w.Body.String())
	}
}

func TestHandleApprovals_NotWired501(t *testing.T) {
	t.Parallel()
	srv := admin.New(admin.Config{Listen: ":0", Enabled: true}, admin.Deps{})
	r := httptest.NewRequest(http.MethodGet, "/admin/approvals", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", w.Code)
	}
}
