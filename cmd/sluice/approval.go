// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/bino-bi/sluice/internal/approval"
	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/secrets"
	"github.com/bino-bi/sluice/internal/transport/admin"
	"github.com/bino-bi/sluice/internal/transport/rest"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// buildApprovalBroker constructs the approval broker when the feature is
// active: at least one ApprovalPolicy is loaded, or a public base URL is
// configured (so the broker is ready for a hot reload that adds policies).
// ApprovalPolicies present without a base URL is a fatal misconfiguration
// (fail-closed — otherwise queries would pend and expire silently).
func buildApprovalBroker(
	ctx context.Context,
	scfg *config.ServerConfig,
	snap *config.Snapshot,
	resolver *secrets.Resolver,
	auditDisp *audit.Dispatcher,
	log *slog.Logger,
) (*approval.Broker, error) {
	hasPolicies := len(snap.ByKind[apitypes.KindApprovalPolicy]) > 0
	baseURL := scfg.Approval.PublicBaseURL

	if hasPolicies && baseURL == "" {
		return nil, fmt.Errorf("approval.publicBaseUrl is required when ApprovalPolicies are loaded")
	}
	if !hasPolicies && baseURL == "" {
		// Feature not in use.
		return nil, nil
	}

	targets, err := resolveWebhookTargets(ctx, scfg.Approval.Webhooks, resolver)
	if err != nil {
		return nil, err
	}
	notifier := approval.NewWebhookNotifier(baseURL, targets, log)

	broker := approval.New(approval.Options{
		Logger:     log,
		Notifier:   notifier,
		Auditor:    &approvalAuditor{disp: auditDisp, origin: hostname()},
		RequestTTL: scfg.Approval.RequestTTL,
		GrantTTL:   scfg.Approval.GrantTTL,
		MaxPending: scfg.Approval.MaxPending,
	})
	return broker, nil
}

// resolveWebhookTargets resolves each webhook's headersRef (a secret://
// reference to a JSON object of header name → value) into a concrete
// header map. Secret bytes live only in the header map.
func resolveWebhookTargets(ctx context.Context, webhooks []config.ApprovalWebhook, resolver *secrets.Resolver) ([]approval.Target, error) {
	out := make([]approval.Target, 0, len(webhooks))
	for _, wh := range webhooks {
		t := approval.Target{URL: wh.URL, Timeout: wh.Timeout}
		if wh.HeadersRef != "" {
			raw, err := resolver.Resolve(ctx, wh.HeadersRef)
			if err != nil {
				return nil, fmt.Errorf("resolve webhook headersRef %q: %w", wh.HeadersRef, err)
			}
			headers := map[string]string{}
			if err := json.Unmarshal(raw, &headers); err != nil {
				return nil, fmt.Errorf("webhook headersRef %q: expected a JSON object: %w", wh.HeadersRef, err)
			}
			t.Headers = headers
		}
		out = append(out, t)
	}
	return out, nil
}

// approvalGateway adapts the concrete broker to the rest.ApprovalGateway
// interface, returning nil when no broker is configured (so the routes stay
// unregistered).
func approvalGateway(b *approval.Broker) rest.ApprovalGateway {
	if b == nil {
		return nil
	}
	return b
}

// adminPendingLister adapts the broker to admin.PendingLister, nil-safe.
func adminPendingLister(b *approval.Broker) admin.PendingLister {
	if b == nil {
		return nil
	}
	return b
}

// approvalAuditor adapts the audit dispatcher to approval.Auditor.
type approvalAuditor struct {
	disp   *audit.Dispatcher
	origin string
}

func (a *approvalAuditor) ApprovalEvent(event string, v approval.View, extra map[string]any) {
	rec := &audit.Record{
		EventType: approvalEventType(event),
		Origin:    a.origin,
		Subject: audit.Subject{
			ID:     v.Subject.ID,
			Issuer: v.Subject.Issuer,
			Email:  v.Subject.Email,
			Groups: v.Subject.Groups,
		},
		Decision: audit.DecisionError, // lifecycle events are not data-serving
		Extras:   map[string]any{"approval_id": v.ID, "sql_hash": v.SQLHash},
	}
	if len(v.Policies) > 0 {
		rec.Extras["approval_policies"] = v.Policies
	}
	for k, val := range extra {
		rec.Extras[k] = val
	}
	// Best-effort: approval lifecycle events never gate data.
	_ = a.disp.Enqueue(context.Background(), rec)
}

func approvalEventType(event string) audit.EventType {
	switch event {
	case "requested":
		return audit.EventApprovalRequested
	case "approved":
		return audit.EventApprovalApproved
	case "rejected":
		return audit.EventApprovalRejected
	case "expired":
		return audit.EventApprovalExpired
	default:
		return audit.EventApprovalRequested
	}
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}

// approvalSyncClamp warns and clamps the sync wait to fit the REST request
// timeout so a hybrid wait actually returns ERR_APPROVAL_PENDING instead of
// being killed by the timeout middleware.
func approvalSyncClamp(scfg *config.ServerConfig, log *slog.Logger) {
	if scfg.Approval.SyncWait <= 0 {
		return
	}
	limit := scfg.REST.RequestTimeout - 2*time.Second
	if limit > 0 && scfg.Approval.SyncWait > limit {
		log.Warn("approval.syncWait exceeds rest.requestTimeout; clamping",
			slog.Duration("syncWait", scfg.Approval.SyncWait),
			slog.Duration("clampedTo", limit))
		scfg.Approval.SyncWait = limit
	}
}
