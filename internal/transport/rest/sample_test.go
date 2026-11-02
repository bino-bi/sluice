// SPDX-License-Identifier: AGPL-3.0-or-later

package rest_test

import (
	"encoding/json"
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

// newSampleServer serves /v1/sample over a service with the real parser,
// which the sample endpoint needs for its shape check.
func newSampleServer(t *testing.T, cfg rest.Config) *rest.Server {
	t.Helper()
	svc := queryservice.New(queryservice.Options{
		Parser:   pgquery.New(parser.Options{}),
		Policy:   &fakePolicy{},
		Rewriter: &fakeRewriter{},
		Executor: &fakeExec{},
		Audit:    &fakeAudit{},
		Clock:    func() time.Time { return time.Unix(1713600000, 0) },
	})
	cfg.Listen = ":0"
	return rest.New(cfg, rest.Deps{
		Service:    svc,
		Identifier: &stubIdentifier{user: &identity.UserCtx{Subject: "x"}},
	})
}

func postSample(srv *rest.Server, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/sample", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(w, r)
	return w
}

func TestHandleSample_JSONHappyPath(t *testing.T) {
	t.Parallel()
	w := postSample(newSampleServer(t, rest.Config{}), `{"table":"shop.main.products","rows":2,"seed":7}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200: body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("X-Query-Id") == "" {
		t.Fatal("X-Query-Id header missing")
	}
	var body struct {
		Columns []string `json:"columns"`
		Rows    [][]any  `json:"rows"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Rows) != 1 {
		t.Fatalf("rows: got %v", body.Rows)
	}
}

func TestHandleSample_CSV(t *testing.T) {
	t.Parallel()
	w := postSample(newSampleServer(t, rest.Config{}), `{"table":"shop.main.products","rows":2,"format":"csv"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200: body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("Content-Type: got %q", ct)
	}
	if !strings.Contains(w.Body.String(), "n\n1\n") {
		t.Fatalf("body: got %q", w.Body.String())
	}
	trailer := w.Result().Trailer
	if got := trailer.Get("X-Sluice-Row-Count"); got != "1" {
		t.Fatalf("X-Sluice-Row-Count trailer: got %q want 1", got)
	}
	if got := trailer.Get("X-Sluice-Truncated"); got != "false" {
		t.Fatalf("X-Sluice-Truncated trailer: got %q want false", got)
	}
}

func TestHandleSample_InvalidJSON400(t *testing.T) {
	t.Parallel()
	w := postSample(newSampleServer(t, rest.Config{}), `{not-json`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400: body=%s", w.Code, w.Body.String())
	}
	if ae := decodeError(t, w.Body); ae.Code != pkgerr.CodeSyntax {
		t.Fatalf("code: got %q want %q", ae.Code, pkgerr.CodeSyntax)
	}
}

func TestHandleSample_UnknownField400(t *testing.T) {
	t.Parallel()
	w := postSample(newSampleServer(t, rest.Config{}), `{"table":"a.b.c","rows":1,"max_rows":5}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400: body=%s", w.Code, w.Body.String())
	}
	if ae := decodeError(t, w.Body); ae.Code != pkgerr.CodeSyntax {
		t.Fatalf("code: got %q want %q", ae.Code, pkgerr.CodeSyntax)
	}
}

func TestHandleSample_BodyCap413(t *testing.T) {
	t.Parallel()
	w := postSample(newSampleServer(t, rest.Config{MaxBodyBytes: 16}), `{"table":"shop.main.products","rows":2}`)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: got %d want 413: body=%s", w.Code, w.Body.String())
	}
}

func TestHandleSample_Unauthorized401(t *testing.T) {
	t.Parallel()
	srv := rest.New(rest.Config{Listen: ":0"}, rest.Deps{
		Identifier: &stubIdentifier{err: identity.ErrNoCredential},
	})
	w := postSample(srv, `{"table":"a.b.c","rows":1}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401", w.Code)
	}
}

func TestHandleSample_Validation400(t *testing.T) {
	t.Parallel()
	w := postSample(newSampleServer(t, rest.Config{}), `{"table":"a.b.c","rows":1,"rate":0.5}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400: body=%s", w.Code, w.Body.String())
	}
	if ae := decodeError(t, w.Body); ae.Code != pkgerr.CodeSyntax {
		t.Fatalf("code: got %q want %q", ae.Code, pkgerr.CodeSyntax)
	}
}
