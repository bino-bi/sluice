// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/approval"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/queryservice"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// TestApproval_PersistsAcrossBrokerRestart parks a query, rebuilds the
// broker from the same SQLite file (a restart), approves via the
// capability token minted before the restart, and proves the re-submitted
// query executes exactly once — then pends again (grant single-use across
// the restart boundary).
func TestApproval_PersistsAcrossBrokerRestart(t *testing.T) {
	received := make(chan map[string]any, 4)
	catcher := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(b, &payload)
		received <- payload
	}))
	defer catcher.Close()

	dbPath := filepath.Join(t.TempDir(), "approvals.db")
	newBrokerOnDB := func() *approval.Broker {
		store, err := approval.NewSQLiteStore(dbPath, nil)
		if err != nil {
			t.Fatalf("NewSQLiteStore: %v", err)
		}
		broker, err := approval.New(approval.Options{
			Notifier: approval.NewWebhookNotifier("https://sluice.example.com",
				[]approval.Target{{URL: catcher.URL}}, nil),
			Store:      store,
			RequestTTL: time.Minute,
			GrantTTL:   time.Minute,
			MaxPending: 10,
		})
		if err != nil {
			t.Fatalf("approval.New: %v", err)
		}
		return broker
	}
	newService := func(broker *approval.Broker, ex *fakeExecutor) *queryservice.Service {
		return queryservice.New(queryservice.Options{
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
	}

	const sql = "SELECT ssn FROM hr.people"

	// 1. Park the query on broker A.
	brokerA := newBrokerOnDB()
	exA := &fakeExecutor{columns: []executor.ColumnInfo{{Name: "ssn"}}, rows: [][]any{{"x"}}}
	svcA := newService(brokerA, exA)
	_, err := svcA.Execute(context.Background(), queryservice.QueryRequest{SQL: sql})
	if ae := pkgerr.FromError(err); ae == nil || ae.Code != pkgerr.CodeApprovalPending {
		t.Fatalf("first submit err = %v, want ERR_APPROVAL_PENDING", err)
	}
	var payload map[string]any
	select {
	case payload = <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("webhook not delivered")
	}
	id, _ := payload["approval_id"].(string)
	token := tokenFromCapabilityURL(t, payload["accept_url"].(string))

	// 2. "Restart": close A, rebuild broker + service from the same DB.
	if err := brokerA.Close(context.Background()); err != nil {
		t.Fatalf("close broker A: %v", err)
	}
	brokerB := newBrokerOnDB()
	defer func() { _ = brokerB.Close(context.Background()) }()
	exB := &fakeExecutor{columns: []executor.ColumnInfo{{Name: "ssn"}}, rows: [][]any{{"x"}}}
	svcB := newService(brokerB, exB)

	// 3. Approve on the restarted broker with the pre-restart token.
	if _, err := brokerB.Accept(id, token, "10.0.0.9"); err != nil {
		t.Fatalf("Accept on restarted broker: %v", err)
	}

	// 4. Identical re-submission consumes the restored grant and executes.
	res, err := svcB.Execute(context.Background(), queryservice.QueryRequest{SQL: sql})
	if err != nil {
		t.Fatalf("re-submit after restart+approval: %v", err)
	}
	_ = res.Rows.Close()
	if exB.execCount != 1 {
		t.Errorf("executor ran %d times, want 1", exB.execCount)
	}

	// 5. The grant stays single-use: the next submission pends again — no
	// webhook re-fire happened for the restored request in between.
	_, err = svcB.Execute(context.Background(), queryservice.QueryRequest{SQL: sql})
	if ae := pkgerr.FromError(err); ae == nil || ae.Code != pkgerr.CodeApprovalPending {
		t.Fatalf("second re-submit err = %v, want ERR_APPROVAL_PENDING", err)
	}
}
