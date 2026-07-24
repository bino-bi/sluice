// SPDX-License-Identifier: AGPL-3.0-or-later

package rest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/datasource"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/internal/transport/rest"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// fakeService satisfies the queryservice.Service shape the REST handler
// uses. We cannot mock *queryservice.Service directly (it is a concrete
// type), so the test spins up a real Service only when it needs to; most
// tests drive the routes via a recording handler stub.
type stubIdentifier struct {
	user *identity.UserCtx
	err  error
}

func (s *stubIdentifier) Identify(_ context.Context, _ *http.Request) (*identity.UserCtx, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.user, nil
}

func (s *stubIdentifier) Name() string { return "stub" }

func TestHandleHealth_Liveness200(t *testing.T) {
	t.Parallel()
	srv := rest.New(rest.Config{Listen: ":0"}, rest.Deps{
		Service:    nil,
		Identifier: &stubIdentifier{user: &identity.UserCtx{Subject: "x"}},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", w.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status field: got %q want ok", body["status"])
	}
}

func TestHandleReady_NoRegistry200(t *testing.T) {
	t.Parallel()
	srv := rest.New(rest.Config{Listen: ":0"}, rest.Deps{
		Identifier: &stubIdentifier{},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/ready", nil)
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", w.Code)
	}
}

func TestHandleReady_DegradedThenHealthy(t *testing.T) {
	t.Parallel()
	reg, err := datasource.New(context.Background(), datasource.Options{
		Snapshot:       &datasource.Snapshot{DataSources: []*apitypes.DataSource{readyDSSpec("ready-ds")}},
		HealthInterval: -1,
	})
	if err != nil {
		t.Fatalf("datasource.New: %v", err)
	}
	t.Cleanup(func() { _ = reg.Close() })

	srv := rest.New(rest.Config{Listen: ":0"}, rest.Deps{
		Identifier: &stubIdentifier{},
		Registry:   reg,
	})

	// Before any probe: fail-closed degraded.
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/ready", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("pre-probe status: got %d want 503", w.Code)
	}
	var body struct {
		Status      string `json:"status"`
		DataSources []struct {
			Healthy bool `json:"healthy"`
		} `json:"datasources"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "degraded" {
		t.Fatalf("pre-probe status field: got %q want degraded", body.Status)
	}

	// After a successful probe: ready.
	if err := reg.Probe(context.Background(), "ready-ds", readyFakePool{}); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/ready", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("post-probe status: got %d want 200: %s", w.Code, w.Body.String())
	}
}

func TestHandleVersion200(t *testing.T) {
	t.Parallel()
	srv := rest.New(rest.Config{Listen: ":0"}, rest.Deps{Identifier: &stubIdentifier{}})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/version", nil)
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["version"]; !ok {
		t.Fatalf("version key missing")
	}
}

func TestHandleQuery_Unauthorized401(t *testing.T) {
	t.Parallel()
	srv := rest.New(rest.Config{Listen: ":0"}, rest.Deps{
		Identifier: &stubIdentifier{err: identity.ErrNoCredential},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/query",
		strings.NewReader(`{"sql":"SELECT 1"}`))
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
		t.Fatalf("WWW-Authenticate: got %q want Bearer", got)
	}
}

func TestHandleQuery_BodyCap413(t *testing.T) {
	t.Parallel()
	srv := rest.New(rest.Config{Listen: ":0", MaxBodyBytes: 16}, rest.Deps{
		Service:    newRealService(t),
		Identifier: &stubIdentifier{user: &identity.UserCtx{Subject: "x"}},
	})
	// The body is longer than 16 bytes.
	body := bytes.NewBufferString(`{"sql":"SELECT 1 AS n"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/query", body)
	r.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge && w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 413 or 400", w.Code)
	}
}

func TestHandleQuery_AcceptCSV(t *testing.T) {
	t.Parallel()
	svc := newRealService(t)
	srv := rest.New(rest.Config{Listen: ":0"}, rest.Deps{
		Service:    svc,
		Identifier: &stubIdentifier{user: &identity.UserCtx{Subject: "x"}},
	})
	body := strings.NewReader(`{"sql":"SELECT 1 AS n"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/query", body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "text/csv")
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200: body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("Content-Type: got %q", ct)
	}
	if !strings.Contains(w.Body.String(), "n\n1\n") {
		t.Fatalf("body: got %q", w.Body.String())
	}
}

func TestHandleQuery_CSVTrailers(t *testing.T) {
	t.Parallel()
	svc := newRealService(t)
	srv := rest.New(rest.Config{Listen: ":0"}, rest.Deps{
		Service:    svc,
		Identifier: &stubIdentifier{user: &identity.UserCtx{Subject: "x"}},
	})
	body := strings.NewReader(`{"sql":"SELECT 1 AS n"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/query", body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "text/csv")
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200: body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Trailer"); !strings.Contains(got, "X-Sluice-Row-Count") {
		t.Fatalf("Trailer declaration missing, got %q", got)
	}
	trailer := w.Result().Trailer
	if got := trailer.Get("X-Sluice-Row-Count"); got != "1" {
		t.Fatalf("X-Sluice-Row-Count trailer: got %q want 1", got)
	}
	if got := trailer.Get("X-Sluice-Truncated"); got != "false" {
		t.Fatalf("X-Sluice-Truncated trailer: got %q want false", got)
	}
	if got := trailer.Get("X-Sluice-Warning"); got != "" {
		t.Fatalf("X-Sluice-Warning trailer: got %q want empty", got)
	}
}

func TestHandleQuery_CSVTrailerTruncated(t *testing.T) {
	t.Parallel()
	svc := newRealService(t)
	srv := rest.New(rest.Config{Listen: ":0"}, rest.Deps{
		Service:    svc,
		Identifier: &stubIdentifier{user: &identity.UserCtx{Subject: "x"}},
	})
	body := strings.NewReader(`{"sql":"SELECT 1 AS trunc"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/query", body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "text/csv")
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200: body=%s", w.Code, w.Body.String())
	}
	trailer := w.Result().Trailer
	if got := trailer.Get("X-Sluice-Truncated"); got != "true" {
		t.Fatalf("X-Sluice-Truncated trailer: got %q want true", got)
	}
	if got := trailer.Get("X-Sluice-Row-Count"); got != "1" {
		t.Fatalf("X-Sluice-Row-Count trailer: got %q want 1", got)
	}
	if got := trailer.Get("X-Sluice-Warning"); got != "ERR_RESULT_TRUNCATED" {
		t.Fatalf("X-Sluice-Warning trailer: got %q want ERR_RESULT_TRUNCATED", got)
	}
}

func TestHandleQuery_JSONTruncatedFlag(t *testing.T) {
	t.Parallel()
	svc := newRealService(t)
	srv := rest.New(rest.Config{Listen: ":0"}, rest.Deps{
		Service:    svc,
		Identifier: &stubIdentifier{user: &identity.UserCtx{Subject: "x"}},
	})
	body := strings.NewReader(`{"sql":"SELECT 1 AS trunc"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/query", body)
	r.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200: body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Truncated bool  `json:"truncated"`
		RowCount  int64 `json:"row_count"`
		Warning   *struct {
			Code string `json:"code"`
		} `json:"warning"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Truncated {
		t.Fatalf("truncated: got false want true (stale-flag regression)")
	}
	if resp.RowCount != 1 {
		t.Fatalf("row_count: got %d want 1", resp.RowCount)
	}
	if resp.Warning == nil || resp.Warning.Code != "ERR_RESULT_TRUNCATED" {
		t.Fatalf("warning: got %+v want code ERR_RESULT_TRUNCATED", resp.Warning)
	}
}

func TestHandleQuery_JSONHappyPath(t *testing.T) {
	t.Parallel()
	svc := newRealService(t)
	srv := rest.New(rest.Config{Listen: ":0"}, rest.Deps{
		Service:    svc,
		Identifier: &stubIdentifier{user: &identity.UserCtx{Subject: "x"}},
	})
	body := strings.NewReader(`{"sql":"SELECT 1 AS id, 'hi' AS name"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/query", body)
	r.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200: body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Query-Id"); got == "" {
		t.Fatalf("X-Query-Id header missing")
	}
	var body2 struct {
		QueryID   string   `json:"query_id"`
		Columns   []string `json:"columns"`
		Rows      [][]any  `json:"rows"`
		RowCount  int64    `json:"row_count"`
		Truncated bool     `json:"truncated"`
		Warning   any      `json:"warning"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body2.Columns) != 2 || body2.Columns[0] != "id" || body2.Columns[1] != "name" {
		t.Fatalf("columns: got %v", body2.Columns)
	}
	if len(body2.Rows) != 1 {
		t.Fatalf("rows: got %d want 1", len(body2.Rows))
	}
	if body2.Warning != nil {
		t.Fatalf("warning: got %v want absent on non-truncated response", body2.Warning)
	}
}

func TestHandleQuery_InvalidJSON400(t *testing.T) {
	t.Parallel()
	srv := rest.New(rest.Config{Listen: ":0"}, rest.Deps{
		Service:    newRealService(t),
		Identifier: &stubIdentifier{user: &identity.UserCtx{Subject: "x"}},
	})
	body := strings.NewReader(`{not-json`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/query", body)
	r.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400: body=%s", w.Code, w.Body.String())
	}
	ae := decodeError(t, w.Body)
	if ae.Code != pkgerr.CodeSyntax {
		t.Fatalf("code: got %q want %q", ae.Code, pkgerr.CodeSyntax)
	}
}

func TestShutdownIsGraceful(t *testing.T) {
	t.Parallel()
	srv := rest.New(rest.Config{
		Listen:          ":0",
		ShutdownTimeout: 500 * time.Millisecond,
	}, rest.Deps{Identifier: &stubIdentifier{}})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// ListenAndServe is not called in this test — we just verify the method
	// terminates cleanly when the context is already done.
	done := make(chan struct{})
	go func() {
		_ = srv.Shutdown(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not return")
	}
}

// newRealService stands up a queryservice backed by fakes that accept any
// SELECT as an allow and execute against DuckDB directly. Used by
// handler integration tests.
func newRealService(t *testing.T) *queryservice.Service {
	t.Helper()
	return newQuerySvc(t)
}

// decodeError reads an APIError JSON body.
func decodeError(t *testing.T, r io.Reader) *pkgerr.APIError {
	t.Helper()
	var ae pkgerr.APIError
	if err := json.NewDecoder(r).Decode(&ae); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	return &ae
}

var _ apitypes.QueryRequest
var _ executor.OutputFormat
