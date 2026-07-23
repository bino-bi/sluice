// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"context"
	"time"
)

// Store persists approval state so pending requests and unconsumed grants
// survive a restart. The broker stays authoritative in memory and calls
// the mutators synchronously while holding its lock; implementations must
// be safe for concurrent use. A nil Store keeps the broker purely
// in-memory (the dev default).
type Store interface {
	// Load returns every persisted request and grant; called once at
	// broker construction to rebuild the in-memory maps.
	Load(ctx context.Context) ([]StoredRequest, []StoredGrant, error)
	// PutRequest upserts a request (insert and every state transition).
	PutRequest(ctx context.Context, r StoredRequest) error
	DeleteRequest(ctx context.Context, id string) error
	PutGrant(ctx context.Context, g StoredGrant) error
	DeleteGrant(ctx context.Context, key string) error
	Close(ctx context.Context) error
}

// StoredRequest is the persisted form of a request. Capability tokens are
// stored as SHA-256 hex — the plaintext never reaches the store, so the
// database (and its backups) cannot mint a working approval link.
type StoredRequest struct {
	ID                string
	SubjectKey        string
	Subject           Subject
	SQLHash           string
	SQLSample         string
	Reasons           []string
	Policies          []string
	AcceptTokenSHA256 string
	RejectTokenSHA256 string
	State             State
	CreatedAt         time.Time
	ExpiresAt         time.Time
	DecidedAt         time.Time
	ApproverIP        string
}

// StoredGrant is the persisted form of a single-use grant.
type StoredGrant struct {
	Key        string // subjectKey + "\x00" + sqlHash
	ApprovalID string
	ExpiresAt  time.Time
}

func (r *request) toStored() StoredRequest {
	return StoredRequest{
		ID:                r.id,
		SubjectKey:        r.subjectKey,
		Subject:           r.subject,
		SQLHash:           r.sqlHash,
		SQLSample:         r.sqlSample,
		Reasons:           r.reasons,
		Policies:          r.policies,
		AcceptTokenSHA256: r.acceptTokenSHA,
		RejectTokenSHA256: r.rejectTokenSHA,
		State:             r.state,
		CreatedAt:         r.createdAt,
		ExpiresAt:         r.expiresAt,
		DecidedAt:         r.decidedAt,
		ApproverIP:        r.approverIP,
	}
}

func fromStored(sr StoredRequest) *request {
	r := &request{
		id:             sr.ID,
		subjectKey:     sr.SubjectKey,
		subject:        sr.Subject,
		sqlHash:        sr.SQLHash,
		sqlSample:      sr.SQLSample,
		reasons:        sr.Reasons,
		policies:       sr.Policies,
		acceptTokenSHA: sr.AcceptTokenSHA256,
		rejectTokenSHA: sr.RejectTokenSHA256,
		state:          sr.State,
		createdAt:      sr.CreatedAt,
		expiresAt:      sr.ExpiresAt,
		decidedAt:      sr.DecidedAt,
		approverIP:     sr.ApproverIP,
		done:           make(chan struct{}),
	}
	if r.state != StatePending {
		close(r.done)
	}
	return r
}
