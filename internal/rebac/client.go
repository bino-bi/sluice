// SPDX-License-Identifier: AGPL-3.0-or-later

// Package rebac evaluates RelationshipPolicy objects against a ReBAC
// backend (OpenFGA) as a policy-engine composite member. A hand-rolled
// ~single-endpoint client keeps the dependency surface minimal.
package rebac

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// RelationChecker answers "does subject have relation to object?".
type RelationChecker interface {
	Check(ctx context.Context, object, relation, subject string) (bool, error)
}

// ClientOptions configures an OpenFGA client.
type ClientOptions struct {
	Endpoint   string
	StoreID    string
	ModelID    string
	Token      []byte
	Timeout    time.Duration
	HTTPClient *http.Client
}

// OpenFGAClient calls a single OpenFGA /check endpoint. Bearer token from
// the secret store is set on every request and never logged.
type OpenFGAClient struct {
	opts   ClientOptions
	client *http.Client
}

// NewOpenFGAClient builds a client.
func NewOpenFGAClient(opts ClientOptions) *OpenFGAClient {
	hc := opts.HTTPClient
	if hc == nil {
		to := opts.Timeout
		if to <= 0 {
			to = 3 * time.Second
		}
		hc = &http.Client{Timeout: to}
	}
	return &OpenFGAClient{opts: opts, client: hc}
}

type checkRequest struct {
	TupleKey             tupleKey `json:"tuple_key"`
	AuthorizationModelID string   `json:"authorization_model_id,omitempty"`
}

type tupleKey struct {
	User     string `json:"user"`
	Relation string `json:"relation"`
	Object   string `json:"object"`
}

type checkResponse struct {
	Allowed bool `json:"allowed"`
}

// Check issues a single POST /stores/{id}/check.
func (c *OpenFGAClient) Check(ctx context.Context, object, relation, subject string) (bool, error) {
	body, err := json.Marshal(checkRequest{
		TupleKey:             tupleKey{User: subject, Relation: relation, Object: object},
		AuthorizationModelID: c.opts.ModelID,
	})
	if err != nil {
		return false, err
	}
	url := strings.TrimRight(c.opts.Endpoint, "/") + "/stores/" + c.opts.StoreID + "/check"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	if len(c.opts.Token) > 0 {
		req.Header.Set("Authorization", "Bearer "+string(c.opts.Token))
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("rebac: openfga check returned %d", resp.StatusCode)
	}
	var out checkResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, fmt.Errorf("rebac: decode check response: %w", err)
	}
	return out.Allowed, nil
}

// Fake is an in-memory RelationChecker for tests. Tuples maps
// "object#relation@subject" → allowed; Err, when set, is returned for
// every call.
type Fake struct {
	Tuples map[string]bool
	Err    error
	Calls  int
}

// Check implements RelationChecker.
func (f *Fake) Check(_ context.Context, object, relation, subject string) (bool, error) {
	f.Calls++
	if f.Err != nil {
		return false, f.Err
	}
	return f.Tuples[object+"#"+relation+"@"+subject], nil
}
