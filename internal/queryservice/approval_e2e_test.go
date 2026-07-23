// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/approval"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/queryservice"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// TestApproval_EndToEndWithRealBroker drives the full loop with the real
// broker + webhook notifier: a query pends and fires a webhook, an
// approver accepts via the captured capability token, the identical
// re-submission executes, and a second re-submission pends again (the
// grant is single-use).
func TestApproval_EndToEndWithRealBroker(t *testing.T) {
	received := make(chan map[string]any, 4)
	catcher := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(b, &payload)
		received <- payload
	}))
	defer catcher.Close()

	notifier := approval.NewWebhookNotifier("https://sluice.example.com",
		[]approval.Target{{URL: catcher.URL}}, nil)
	broker := approval.New(approval.Options{
		Notifier:   notifier,
		RequestTTL: time.Minute,
		GrantTTL:   time.Minute,
		MaxPending: 10,
	})

	ex := &fakeExecutor{columns: []executor.ColumnInfo{{Name: "ssn"}}, rows: [][]any{{"x"}}}
	svc := queryservice.New(queryservice.Options{
		Parser:           &fakeParser{},
		Policy:           &fakePolicy{decision: approvalDecision()},
		Rewriter:         &fakeRewriter{},
		Executor:         ex,
		Audit:            &fakeAudit{},
		Clock:            func() time.Time { return time.Unix(1713600000, 0) },
		Approvals:        broker,
		ApprovalSyncWait: 30 * time.Millisecond,
		Limits:           queryservice.Limits{ApprovalSQLSampleBytes: 2048},
	})

	const sql = "SELECT ssn FROM hr.people"

	// 1. First submission pends and fires the webhook.
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: sql})
	if ae := pkgerr.FromError(err); ae == nil || ae.Code != pkgerr.CodeApprovalPending {
		t.Fatalf("first submit err = %v, want ERR_APPROVAL_PENDING", err)
	}

	var payload map[string]any
	select {
	case payload = <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("webhook not delivered")
	}
	// The approver must see the SQL they are approving.
	if got, _ := payload["sql"].(string); got != sql {
		t.Fatalf("webhook sql = %q, want %q", got, sql)
	}
	id, _ := payload["approval_id"].(string)
	acceptURL, _ := payload["accept_url"].(string)
	token := tokenFromCapabilityURL(t, acceptURL)

	// 2. Approver accepts via the capability token.
	if _, err := broker.Accept(id, token, "10.0.0.9"); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	// 3. Identical re-submission consumes the grant and executes.
	res, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: sql})
	if err != nil {
		t.Fatalf("re-submit after approval: %v", err)
	}
	_ = res.Rows.Close()
	if ex.execCount != 1 {
		t.Errorf("executor ran %d times, want 1", ex.execCount)
	}

	// 4. A second re-submission pends again — the grant was single-use.
	_, err = svc.Execute(context.Background(), queryservice.QueryRequest{SQL: sql})
	if ae := pkgerr.FromError(err); ae == nil || ae.Code != pkgerr.CodeApprovalPending {
		t.Fatalf("second re-submit err = %v, want ERR_APPROVAL_PENDING (grant single-use)", err)
	}
}

func TestApproval_EndToEndRejectPath(t *testing.T) {
	received := make(chan map[string]any, 4)
	catcher := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var p map[string]any
		_ = json.Unmarshal(b, &p)
		received <- p
	}))
	defer catcher.Close()

	broker := approval.New(approval.Options{
		Notifier:   approval.NewWebhookNotifier("https://x", []approval.Target{{URL: catcher.URL}}, nil),
		RequestTTL: time.Minute,
	})
	svc := queryservice.New(queryservice.Options{
		Parser:           &fakeParser{},
		Policy:           &fakePolicy{decision: approvalDecision()},
		Rewriter:         &fakeRewriter{},
		Executor:         &fakeExecutor{},
		Audit:            &fakeAudit{},
		Clock:            func() time.Time { return time.Unix(1713600000, 0) },
		Approvals:        broker,
		ApprovalSyncWait: 20 * time.Millisecond,
	})

	_, _ = svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT ssn FROM t"})
	p := <-received
	// ApprovalSQLSampleBytes is unset here: 0 disables the sample — there
	// is no hidden fallback.
	if got, _ := p["sql"].(string); got != "" {
		t.Fatalf("webhook sql = %q, want empty when sampling disabled", got)
	}
	id, _ := p["approval_id"].(string)
	token := tokenFromCapabilityURL(t, p["reject_url"].(string))

	if _, err := broker.Reject(id, token, ""); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	// A re-submission after rejection pends again (no grant); reject only
	// affects the specific request, and re-submission mints a new one.
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT ssn FROM t"})
	if ae := pkgerr.FromError(err); ae == nil || ae.Code != pkgerr.CodeApprovalPending {
		t.Fatalf("post-reject re-submit err = %v, want ERR_APPROVAL_PENDING", err)
	}
	_ = policy.OutcomeAllow
}

func tokenFromCapabilityURL(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse capability URL %q: %v", raw, err)
	}
	tok := u.Query().Get("token")
	if tok == "" || !strings.Contains(raw, "/approvals/") {
		t.Fatalf("capability URL missing token: %q", raw)
	}
	return tok
}
