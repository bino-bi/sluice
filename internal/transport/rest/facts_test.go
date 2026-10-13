// SPDX-License-Identifier: AGPL-3.0-or-later

package rest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/internal/transport/rest"
	pkgapi "github.com/bino-bi/sluice/pkg/apitypes"
)

// capturePolicy keeps the input of the last evaluation.
type capturePolicy struct{ in policy.Input }

func (p *capturePolicy) Evaluate(_ context.Context, in policy.Input) (*policy.Decision, error) {
	p.in = in
	return &policy.Decision{Outcome: policy.OutcomeAllow}, nil
}

func (p *capturePolicy) Explain(context.Context, policy.Input) (*pkgapi.ExplainResult, error) {
	return &pkgapi.ExplainResult{}, nil
}

// The policy sees the address, the user agent and the headers of the
// request as request.*, but no credential.
func TestHandleQuery_FillsRequestFacts(t *testing.T) {
	t.Parallel()
	pol := &capturePolicy{}
	svc := queryservice.New(queryservice.Options{
		Parser:   &fakeParser{},
		Policy:   pol,
		Rewriter: &fakeRewriter{},
		Executor: &fakeExec{},
		Audit:    &fakeAudit{},
		Clock:    func() time.Time { return time.Unix(1713600000, 0) },
	})
	srv := rest.New(rest.Config{Listen: ":0"}, rest.Deps{
		Service:    svc,
		Identifier: &stubIdentifier{user: &identity.UserCtx{Subject: "x"}},
	})
	r := httptest.NewRequest(http.MethodPost, "/v1/query", strings.NewReader(`{"sql":"SELECT 1"}`))
	r.RemoteAddr = "10.1.2.3:4711"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", "bi-tool/2")
	r.Header.Set("X-Region", "eu")
	r.Header.Set("X-Api-Key", "k.secret")
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	f := pol.in.Request
	if f == nil {
		t.Fatal("the policy got no request facts")
	}
	if got := f.RemoteIP.String(); got != "10.1.2.3" {
		t.Errorf("remote_ip = %q, want 10.1.2.3", got)
	}
	if f.UserAgent != "bi-tool/2" || f.Headers["x-region"] != "eu" {
		t.Errorf("user_agent = %q, headers = %v", f.UserAgent, f.Headers)
	}
	for name, value := range f.Headers {
		if strings.Contains(value, "secret") {
			t.Errorf("header %q passed a credential to the policy", name)
		}
	}
}
