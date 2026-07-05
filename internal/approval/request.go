// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import "time"

// State is the lifecycle state of an approval request.
type State string

// State values.
const (
	StatePending  State = "pending"
	StateApproved State = "approved"
	StateRejected State = "rejected"
	StateExpired  State = "expired"
	StateConsumed State = "consumed"
)

// Subject is the identity snapshot carried in the webhook payload.
type Subject struct {
	ID     string   `json:"id"`
	Issuer string   `json:"issuer,omitempty"`
	Email  string   `json:"email,omitempty"`
	Groups []string `json:"groups,omitempty"`
}

// RequireInput is the data the queryservice hands the broker when a query
// needs approval.
type RequireInput struct {
	Subject    Subject
	SubjectKey string // issuer\x00subject; drives dedupe + grant ownership
	SQLHash    string // sha256 hex of the raw SQL
	SQLSample  string // capped SQL text for the webhook payload
	Reasons    []string
	Policies   []string
}

// Ticket is returned by Require: the id the caller waits on.
type Ticket struct {
	ID        string
	State     State
	ExpiresAt time.Time
}

// View is a token-free snapshot of a request for status polls and admin.
type View struct {
	ID        string    `json:"approval_id"`
	State     State     `json:"state"`
	Subject   Subject   `json:"subject"`
	SQLHash   string    `json:"sql_hash"`
	SQLSample string    `json:"sql_sample,omitempty"`
	Reasons   []string  `json:"reasons,omitempty"`
	Policies  []string  `json:"policies,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	DecidedAt time.Time `json:"decided_at,omitempty"`
}

// DecisionResult is returned by Accept/Reject.
type DecisionResult struct {
	State          State
	AlreadyDecided bool
}

// request is the internal, mutable record. Tokens never leave the broker.
type request struct {
	id          string
	subjectKey  string
	subject     Subject
	sqlHash     string
	sqlSample   string
	reasons     []string
	policies    []string
	acceptToken string
	rejectToken string
	state       State
	createdAt   time.Time
	expiresAt   time.Time
	decidedAt   time.Time
	approverIP  string
	done        chan struct{} // closed exactly once when decided or expired
}

func (r *request) view() View {
	return View{
		ID:        r.id,
		State:     r.state,
		Subject:   r.subject,
		SQLHash:   r.sqlHash,
		SQLSample: r.sqlSample,
		Reasons:   r.reasons,
		Policies:  r.policies,
		CreatedAt: r.createdAt,
		ExpiresAt: r.expiresAt,
		DecidedAt: r.decidedAt,
	}
}

// grant is a single-use permission to run one approved query.
type grant struct {
	approvalID string
	expiresAt  time.Time
}
