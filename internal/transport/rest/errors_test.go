// SPDX-License-Identifier: AGPL-3.0-or-later

package rest_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/approval"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/internal/transport/rest"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

type failingExec struct{ err error }

func (e failingExec) Execute(context.Context, executor.Request) (*executor.Response, error) {
	return nil, e.err
}

// The client of an ERR_INTERNAL only gets the code. The cause and the
// query_id must be in the log. A client error logs nothing at ERROR.
func TestHandleQuery_InternalErrorIsLogged(t *testing.T) {
	t.Parallel()
	var log bytes.Buffer
	srv := rest.New(rest.Config{Listen: ":0"}, rest.Deps{
		Service: queryservice.New(queryservice.Options{
			Parser:   &fakeParser{},
			Policy:   &fakePolicy{},
			Rewriter: &fakeRewriter{},
			Executor: failingExec{err: errors.New("IO Error: disk")},
			Audit:    &fakeAudit{},
		}),
		Identifier: &stubIdentifier{user: &identity.UserCtx{Subject: "x"}},
		Logger:     slog.New(slog.NewJSONHandler(&log, nil)),
	})
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/v1/query", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		srv.Handler().ServeHTTP(w, r)
		return w
	}

	w := post(`{"sql":"SELECT 1 AS id"}`)
	ae := decodeError(t, w.Body)
	if w.Code != http.StatusInternalServerError || ae.Code != pkgerr.CodeInternal || ae.QueryID == "" {
		t.Fatalf("status = %d, error = %+v, want 500 ERR_INTERNAL with a query_id", w.Code, ae)
	}
	if strings.Contains(ae.Message, "disk") {
		t.Fatalf("message = %q, the cause must not reach the client", ae.Message)
	}
	got := log.String()
	for _, want := range []string{`"level":"ERROR"`, `"query_id":"` + ae.QueryID + `"`, "IO Error: disk"} {
		if !strings.Contains(got, want) {
			t.Fatalf("log = %q, want it to contain %q", got, want)
		}
	}

	log.Reset()
	if w := post(`{not-json`); w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if strings.Contains(log.String(), `"level":"ERROR"`) {
		t.Fatalf("log = %q, want no ERROR line for a client error", log.String())
	}
}

type failingGateway struct{ gwBroker }

func (failingGateway) Accept(string, string, string) (approval.DecisionResult, error) {
	return approval.DecisionResult{}, errors.New("approval store: disk full")
}

// A failed approval decision answers 500 without detail. The cause must be
// in the log.
func TestApprovalDecision_FailureIsLogged(t *testing.T) {
	t.Parallel()
	var log bytes.Buffer
	srv := rest.New(rest.Config{Listen: ":0"}, rest.Deps{
		Identifier: &stubIdentifier{user: &identity.UserCtx{Subject: "alice"}},
		Approvals:  &failingGateway{},
		Logger:     slog.New(slog.NewJSONHandler(&log, nil)),
	})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/approvals/apr_1/accept?token=acc", nil))
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "disk") {
		t.Fatalf("status = %d, body = %s, want 500 without the cause", w.Code, w.Body.String())
	}
	got := log.String()
	for _, want := range []string{`"level":"ERROR"`, `"approval_id":"apr_1"`, "approval store: disk full"} {
		if !strings.Contains(got, want) {
			t.Fatalf("log = %q, want it to contain %q", got, want)
		}
	}
}
