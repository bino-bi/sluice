// SPDX-License-Identifier: AGPL-3.0-or-later

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/internal/transport/admin"
	pkgapi "github.com/bino-bi/sluice/pkg/apitypes"
)

// ---- fakes identical to those used by the REST / MCP transports -------

type fakeAST struct{}

func (a *fakeAST) Raw() any                   { return nil }
func (a *fakeAST) Fingerprint() string        { return "" }
func (a *fakeAST) Tables() []parser.TableRef  { return nil }
func (a *fakeAST) Catalogs() []string         { return nil }
func (a *fakeAST) Shape() parser.QueryShape   { return parser.QueryShape{} }
func (a *fakeAST) Clone() parser.AST          { return a }
func (a *fakeAST) Statement() parser.StmtKind { return parser.StmtSelect }
func (a *fakeAST) Source() string             { return "" }

type fakeParser struct{}

func (p *fakeParser) Parse(_ context.Context, _ string) (parser.AST, error) {
	return &fakeAST{}, nil
}
func (p *fakeParser) Deparse(_ context.Context, _ parser.AST) (string, error) { return "", nil }
func (p *fakeParser) Fingerprint(_ string) (string, error)                    { return "", nil }
func (p *fakeParser) Name() string                                            { return "fake" }

type fakePolicy struct{}

func (p *fakePolicy) Evaluate(_ context.Context, _ policy.Input) (*policy.Decision, error) {
	return &policy.Decision{Outcome: policy.OutcomeAllow}, nil
}
func (p *fakePolicy) Explain(_ context.Context, _ policy.Input) (*pkgapi.ExplainResult, error) {
	return &pkgapi.ExplainResult{
		Subject:   "alice",
		Resource:  "pg.public.orders",
		Effective: pkgapi.EffectiveDecision{Decision: "allow"},
	}, nil
}

type fakeRewriter struct{}

func (r *fakeRewriter) Rewrite(_ context.Context, _ rewriter.RewriteRequest) (*rewriter.RewriteResult, error) {
	return &rewriter.RewriteResult{}, nil
}

type fakeExec struct{}

func (e *fakeExec) Execute(_ context.Context, _ executor.Request) (*executor.Response, error) {
	return nil, errors.New("not used")
}

type fakeAudit struct {
	mu      sync.Mutex
	records []*audit.Record
}

func (a *fakeAudit) Enqueue(_ context.Context, r *audit.Record) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.records = append(a.records, r)
	return nil
}

func newSvc(t *testing.T) *queryservice.Service {
	t.Helper()
	return queryservice.New(queryservice.Options{
		Parser:   &fakeParser{},
		Policy:   &fakePolicy{},
		Rewriter: &fakeRewriter{},
		Executor: &fakeExec{},
		Audit:    &fakeAudit{},
		Clock:    func() time.Time { return time.Unix(1713600000, 0) },
	})
}

// ---- tests ----

func TestHandlePolicies_NoEngine200EmptySet(t *testing.T) {
	t.Parallel()
	srv := admin.New(admin.Config{Listen: ":0", Enabled: true}, admin.Deps{})
	r := httptest.NewRequest(http.MethodGet, "/admin/policies", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["policies"]; !ok {
		t.Fatalf("policies key missing")
	}
}

func TestHandlePolicies_AuthRequired(t *testing.T) {
	t.Parallel()
	srv := admin.New(admin.Config{Listen: ":0", Enabled: true, Token: "secret"}, admin.Deps{})
	r := httptest.NewRequest(http.MethodGet, "/admin/policies", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401", w.Code)
	}
}

func TestHandlePolicies_AuthSuccess(t *testing.T) {
	t.Parallel()
	srv := admin.New(admin.Config{Listen: ":0", Enabled: true, Token: "secret"}, admin.Deps{})
	r := httptest.NewRequest(http.MethodGet, "/admin/policies", nil)
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", w.Code)
	}
}

func TestHandleDataSources_NoRegistry200EmptyList(t *testing.T) {
	t.Parallel()
	srv := admin.New(admin.Config{Listen: ":0", Enabled: true}, admin.Deps{})
	r := httptest.NewRequest(http.MethodGet, "/admin/datasources", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", w.Code)
	}
}

func TestHandleExplain_HappyPath(t *testing.T) {
	t.Parallel()
	srv := admin.New(admin.Config{Listen: ":0", Enabled: true}, admin.Deps{
		Service: newSvc(t),
	})
	r := httptest.NewRequest(http.MethodGet,
		"/admin/subjects/explain?user=alice&table=pg.public.orders", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200: body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"decision":"allow"`) {
		t.Fatalf("body missing decision: %s", body)
	}
}

func TestHandleExplain_MissingUserReturns400(t *testing.T) {
	t.Parallel()
	srv := admin.New(admin.Config{Listen: ":0", Enabled: true}, admin.Deps{
		Service: newSvc(t),
	})
	r := httptest.NewRequest(http.MethodGet,
		"/admin/subjects/explain?table=pg.public.orders", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400", w.Code)
	}
}

func TestHandleReload_NotWired501(t *testing.T) {
	t.Parallel()
	srv := admin.New(admin.Config{Listen: ":0", Enabled: true}, admin.Deps{})
	r := httptest.NewRequest(http.MethodPost, "/admin/reload", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status: got %d want 501", w.Code)
	}
}

type fakeReloader struct {
	called int
	err    error
}

func (f *fakeReloader) Reload(context.Context) error { f.called++; return f.err }

func TestHandleReload_Wired200(t *testing.T) {
	t.Parallel()
	rl := &fakeReloader{}
	srv := admin.New(admin.Config{Listen: ":0", Enabled: true}, admin.Deps{
		Reloader: rl,
	})
	r := httptest.NewRequest(http.MethodPost, "/admin/reload", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200: body=%s", w.Code, w.Body.String())
	}
	if rl.called != 1 {
		t.Fatalf("reloader called %d times, want 1", rl.called)
	}
}

func TestHandleReload_PolicyErrorReturnsPolicyInvalid(t *testing.T) {
	t.Parallel()
	rl := &fakeReloader{err: fmt.Errorf("reload: %w", policy.ErrSnapshotInvalid)}
	srv := admin.New(admin.Config{Listen: ":0", Enabled: true}, admin.Deps{
		Reloader: rl,
	})
	r := httptest.NewRequest(http.MethodPost, "/admin/reload", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400: body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "ERR_POLICY_INVALID") {
		t.Fatalf("body missing ERR_POLICY_INVALID: %s", w.Body.String())
	}
}

func TestHandleReload_GenericErrorReturnsConfigInvalid(t *testing.T) {
	t.Parallel()
	rl := &fakeReloader{err: fmt.Errorf("directory unreadable")}
	srv := admin.New(admin.Config{Listen: ":0", Enabled: true}, admin.Deps{
		Reloader: rl,
	})
	r := httptest.NewRequest(http.MethodPost, "/admin/reload", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400: body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "ERR_CONFIG_INVALID") {
		t.Fatalf("body missing ERR_CONFIG_INVALID: %s", w.Body.String())
	}
}

type stubTailer struct{ recs []*audit.Record }

func (s *stubTailer) Tail(_ context.Context, n int) ([]*audit.Record, error) {
	if n > len(s.recs) {
		n = len(s.recs)
	}
	return s.recs[:n], nil
}

func TestHandleAuditTail_NDJSON(t *testing.T) {
	t.Parallel()
	tailer := &stubTailer{recs: []*audit.Record{
		{QueryID: "q1", Decision: "allow"},
		{QueryID: "q2", Decision: "deny"},
	}}
	srv := admin.New(admin.Config{Listen: ":0", Enabled: true}, admin.Deps{Audit: tailer})
	r := httptest.NewRequest(http.MethodGet, "/admin/audit/tail?n=2", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200: body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/x-ndjson") {
		t.Fatalf("Content-Type: got %q", ct)
	}
	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines: got %d want 2: body=%s", len(lines), w.Body.String())
	}
}

func TestHandleHealthz_OK(t *testing.T) {
	t.Parallel()
	srv := admin.New(admin.Config{Listen: ":0", Enabled: true}, admin.Deps{})
	r := httptest.NewRequest(http.MethodGet, "/admin/healthz", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", w.Code)
	}
}

func TestHandleVersion_OK(t *testing.T) {
	t.Parallel()
	srv := admin.New(admin.Config{Listen: ":0", Enabled: true}, admin.Deps{})
	r := httptest.NewRequest(http.MethodGet, "/admin/version", nil)
	w := httptest.NewRecorder()
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

func TestHandleMetrics_AuthRequired(t *testing.T) {
	t.Parallel()
	srv := admin.New(admin.Config{Listen: ":0", Enabled: true, Token: "secret"}, admin.Deps{})
	r := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401", w.Code)
	}
}

func TestHandleMetrics_PrometheusText(t *testing.T) {
	t.Parallel()
	srv := admin.New(admin.Config{Listen: ":0", Enabled: true, Token: "secret"}, admin.Deps{})
	r := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type: got %q want text/plain prefix", ct)
	}
	if !strings.Contains(w.Body.String(), "go_goroutines") {
		t.Fatalf("body missing default runtime metrics: %.200s", w.Body.String())
	}
}

func TestRequestIDHeaderAlwaysSet(t *testing.T) {
	t.Parallel()
	srv := admin.New(admin.Config{Listen: ":0", Enabled: true}, admin.Deps{})
	r := httptest.NewRequest(http.MethodGet, "/admin/version", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if got := w.Header().Get("X-Admin-Request-Id"); got == "" {
		t.Fatalf("missing X-Admin-Request-Id header")
	}
}

type failingTailer struct{ err error }

func (f failingTailer) Tail(context.Context, int) ([]*audit.Record, error) { return nil, f.err }

// The client of an ERR_INTERNAL only gets the code, so the cause must be
// in the log.
func TestHandleAuditTail_InternalErrorIsLogged(t *testing.T) {
	t.Parallel()
	var log bytes.Buffer
	srv := admin.New(admin.Config{Listen: ":0", Enabled: true}, admin.Deps{
		Audit:  failingTailer{err: errors.New("audit file: permission denied")},
		Logger: slog.New(slog.NewJSONHandler(&log, nil)),
	})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/audit/tail", nil))
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "ERR_INTERNAL") || strings.Contains(w.Body.String(), "permission") {
		t.Fatalf("status = %d, body = %s, want 500 ERR_INTERNAL without the cause", w.Code, w.Body.String())
	}
	got := log.String()
	for _, want := range []string{`"level":"ERROR"`, "audit file: permission denied"} {
		if !strings.Contains(got, want) {
			t.Fatalf("log = %q, want it to contain %q", got, want)
		}
	}
}

func TestAdminAuth_FailedLoginIsReported(t *testing.T) {
	t.Parallel()
	type call struct{ ip, reason string }
	var calls []call
	srv := admin.New(admin.Config{Listen: ":0", Enabled: true, Token: "secret"}, admin.Deps{
		AuthFailed: func(ip, reason string) { calls = append(calls, call{ip, reason}) },
	})
	for _, auth := range []string{"", "Bearer wrong", "Bearer secret"} {
		r := httptest.NewRequest(http.MethodGet, "/admin/policies", nil)
		r.RemoteAddr = "203.0.113.7:51234"
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		srv.Handler().ServeHTTP(httptest.NewRecorder(), r)
	}
	want := []call{{"203.0.113.7", "no credential"}, {"203.0.113.7", "invalid credential"}}
	if len(calls) != 2 || calls[0] != want[0] || calls[1] != want[1] {
		t.Errorf("calls = %v, want %v", calls, want)
	}
}
