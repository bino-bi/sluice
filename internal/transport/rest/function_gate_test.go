// SPDX-License-Identifier: AGPL-3.0-or-later

package rest_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/internal/transport/rest"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// The function gate lives in the query service. This locks that the REST
// path reaches it: real parser, policy that allows everything.
func TestHandleQuery_FunctionGateRejects403(t *testing.T) {
	t.Parallel()
	ex := &fakeExec{}
	svc := queryservice.New(queryservice.Options{
		Parser:   pgquery.New(parser.Options{}),
		Policy:   &fakePolicy{},
		Rewriter: &fakeRewriter{},
		Executor: ex,
		Audit:    &fakeAudit{},
		Clock:    func() time.Time { return time.Unix(1713600000, 0) },
	})
	srv := rest.New(rest.Config{Listen: ":0"}, rest.Deps{
		Service:    svc,
		Identifier: &stubIdentifier{user: &identity.UserCtx{Subject: "x"}},
	})
	body := strings.NewReader(`{"sql":"SELECT t.id FROM (SELECT 1 FROM shop.main.customers LIMIT 1) x, query_table('shop.main.customers') t"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/query", body)
	r.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status: got %d want 403: body=%s", w.Code, w.Body.String())
	}
	ae := decodeError(t, w.Body)
	if ae.Code != pkgerr.CodeACLRejected {
		t.Fatalf("code = %s, want %s", ae.Code, pkgerr.CodeACLRejected)
	}
	if !strings.Contains(ae.Message, "query_table") {
		t.Fatalf("message %q should name the function", ae.Message)
	}
}
