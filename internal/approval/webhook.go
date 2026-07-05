// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Target is one webhook endpoint. Headers are pre-resolved from secret://
// references at boot/reload; secret bytes live only in the header map and
// are never logged.
type Target struct {
	URL     string
	Headers map[string]string
	Timeout time.Duration
}

// WebhookNotifier delivers approval requests to one or more targets. It
// follows the internal/identity/jwks.go client discipline: an explicit
// timeout and context-scoped requests.
type WebhookNotifier struct {
	targets       []Target
	publicBaseURL string
	client        *http.Client
	logger        *slog.Logger
}

// NewWebhookNotifier builds a notifier. publicBaseURL is the externally
// reachable base (e.g. https://sluice.example.com) used to build the
// accept/reject capability URLs.
func NewWebhookNotifier(publicBaseURL string, targets []Target, logger *slog.Logger) *WebhookNotifier {
	if logger == nil {
		logger = slog.Default()
	}
	registerMetrics()
	return &WebhookNotifier{
		targets:       targets,
		publicBaseURL: strings.TrimRight(publicBaseURL, "/"),
		client:        &http.Client{Timeout: 10 * time.Second},
		logger:        logger,
	}
}

// payload is the webhook body.
type payload struct {
	ApprovalID  string   `json:"approval_id"`
	Subject     Subject  `json:"subject"`
	SQL         string   `json:"sql"`
	Reasons     []string `json:"reasons,omitempty"`
	Policies    []string `json:"policies,omitempty"`
	AcceptURL   string   `json:"accept_url"`
	RejectURL   string   `json:"reject_url"`
	RequestedAt string   `json:"requested_at"`
	ExpiresAt   string   `json:"expires_at"`
}

// Notify fires every target asynchronously. Delivery failure never fails
// the query: the request stays pending until its TTL, with a loud log and
// a failure metric.
func (n *WebhookNotifier) Notify(v View, acceptToken, rejectToken string) {
	body := payload{
		ApprovalID:  v.ID,
		Subject:     v.Subject,
		SQL:         v.SQLSample,
		Reasons:     v.Reasons,
		Policies:    v.Policies,
		AcceptURL:   n.capabilityURL(v.ID, "accept", acceptToken),
		RejectURL:   n.capabilityURL(v.ID, "reject", rejectToken),
		RequestedAt: v.CreatedAt.UTC().Format(time.RFC3339),
		ExpiresAt:   v.ExpiresAt.UTC().Format(time.RFC3339),
	}
	raw, err := json.Marshal(body)
	if err != nil {
		n.logger.Error("approval webhook: marshal failed", slog.String("approval_id", v.ID), slog.String("error", err.Error()))
		return
	}
	for _, t := range n.targets {
		go n.deliver(t, v.ID, raw)
	}
}

// deliver posts to one target with bounded retries and exponential backoff.
func (n *WebhookNotifier) deliver(t Target, approvalID string, raw []byte) {
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	const attempts = 3
	backoff := time.Second
	for attempt := 1; attempt <= attempts; attempt++ {
		if n.post(t, timeout, raw) {
			mWebhook.WithLabelValues("delivered").Inc()
			return
		}
		mWebhook.WithLabelValues("retry").Inc()
		if attempt < attempts {
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	mWebhook.WithLabelValues("failed").Inc()
	// No token in the log — only the id and target URL.
	n.logger.Error("approval webhook: delivery failed after retries; request stays pending until TTL",
		slog.String("approval_id", approvalID), slog.String("target", t.URL))
}

func (n *WebhookNotifier) post(t Target, timeout time.Duration, raw []byte) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL, bytes.NewReader(raw))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// capabilityURL builds a public accept/reject URL carrying the token.
func (n *WebhookNotifier) capabilityURL(id, verb, token string) string {
	return n.publicBaseURL + "/v1/approvals/" + id + "/" + verb + "?token=" + token
}
