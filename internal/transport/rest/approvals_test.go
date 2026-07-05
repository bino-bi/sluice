// SPDX-License-Identifier: AGPL-3.0-or-later

package rest_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/approval"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/transport/rest"
)

// gwBroker is a fake ApprovalGateway.
type gwBroker struct {
	acceptToken string
	rejectToken string
	view        approval.View
	found       bool
	accepted    int
	rejected    int
}

func (b *gwBroker) Accept(id, token, _ string) (approval.DecisionResult, error) {
	if id != b.view.ID {
		return approval.DecisionResult{}, approval.ErrNotFound
	}
	if token != b.acceptToken {
		return approval.DecisionResult{}, approval.ErrTokenMismatch
	}
	b.accepted++
	return approval.DecisionResult{State: approval.StateApproved}, nil
}

func (b *gwBroker) Reject(id, token, _ string) (approval.DecisionResult, error) {
	if id != b.view.ID {
		return approval.DecisionResult{}, approval.ErrNotFound
	}
	if token != b.rejectToken {
		return approval.DecisionResult{}, approval.ErrTokenMismatch
	}
	b.rejected++
	return approval.DecisionResult{State: approval.StateRejected}, nil
}

func (b *gwBroker) Get(id string) (approval.View, bool) {
	if !b.found || id != b.view.ID {
		return approval.View{}, false
	}
	return b.view, true
}

func newApprovalServer(b *gwBroker) *rest.Server {
	return rest.New(rest.Config{Listen: ":0"}, rest.Deps{
		Identifier: &stubIdentifier{user: &identity.UserCtx{Subject: "alice"}},
		Approvals:  b,
	})
}

func TestApprovalAccept_HappyPath(t *testing.T) {
	b := &gwBroker{acceptToken: "acc", view: approval.View{ID: "apr_1"}, found: true}
	srv := newApprovalServer(b)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/approvals/apr_1/accept?token=acc", nil)
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if b.accepted != 1 {
		t.Errorf("accept not recorded")
	}
}

func TestApprovalReject_POST(t *testing.T) {
	b := &gwBroker{rejectToken: "rej", view: approval.View{ID: "apr_1"}, found: true}
	srv := newApprovalServer(b)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/approvals/apr_1/reject?token=rej", nil)
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK || b.rejected != 1 {
		t.Fatalf("status=%d rejected=%d", w.Code, b.rejected)
	}
}

func TestApproval_OracleFreeness(t *testing.T) {
	// Unknown id and bad token must produce byte-identical 404 responses.
	b := &gwBroker{acceptToken: "acc", view: approval.View{ID: "apr_1"}, found: true}
	srv := newApprovalServer(b)

	unknown := httptest.NewRecorder()
	srv.Handler().ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/v1/approvals/nope/accept?token=acc", nil))

	badTok := httptest.NewRecorder()
	srv.Handler().ServeHTTP(badTok, httptest.NewRequest(http.MethodGet, "/v1/approvals/apr_1/accept?token=wrong", nil))

	if unknown.Code != http.StatusNotFound || badTok.Code != http.StatusNotFound {
		t.Fatalf("codes: unknown=%d badTok=%d, want both 404", unknown.Code, badTok.Code)
	}
	if unknown.Body.String() != badTok.Body.String() {
		t.Errorf("404 bodies differ (oracle): %q vs %q", unknown.Body.String(), badTok.Body.String())
	}
}

func TestApproval_PrefetchDoesNotMutate(t *testing.T) {
	b := &gwBroker{acceptToken: "acc", view: approval.View{ID: "apr_1"}, found: true}
	srv := newApprovalServer(b)

	// HEAD must not mutate.
	head := httptest.NewRecorder()
	srv.Handler().ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/v1/approvals/apr_1/accept?token=acc", nil))

	// Prefetch header must not mutate.
	pf := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/approvals/apr_1/accept?token=acc", nil)
	r.Header.Set("Purpose", "prefetch")
	srv.Handler().ServeHTTP(pf, r)

	if b.accepted != 0 {
		t.Errorf("prefetch/HEAD mutated state: accepted=%d", b.accepted)
	}
}

func TestApproval_StatusOwnerOnly(t *testing.T) {
	// stubIdentifier authenticates subject "alice", issuer "". The owner
	// key is derived from the View's Subject fields.
	b := &gwBroker{found: true, view: approval.View{
		ID: "apr_1", State: approval.StatePending, ExpiresAt: time.Unix(1_700_000_900, 0),
		Subject: approval.Subject{ID: "alice"},
	}}
	srv := newApprovalServer(b)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/approvals/apr_1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("owner status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["approval_id"] != "apr_1" {
		t.Errorf("status body wrong: %v", body)
	}

	// Foreign subject → 404.
	b.view.Subject = approval.Subject{ID: "mallory", Issuer: "iss"}
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/v1/approvals/apr_1", nil))
	if w2.Code != http.StatusNotFound {
		t.Errorf("foreign status = %d, want 404", w2.Code)
	}
}
