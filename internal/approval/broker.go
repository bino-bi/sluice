// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// Notifier delivers an approval request to the configured webhooks. It is
// invoked once, asynchronously, when a request is first created.
type Notifier interface {
	Notify(v View, acceptURL, rejectURL string)
}

// Auditor records approval lifecycle events. All calls are best-effort;
// the data-serving audit record is emitted separately by queryservice.
type Auditor interface {
	ApprovalEvent(event string, v View, extra map[string]any)
}

// Options configures a Broker.
type Options struct {
	Clock      func() time.Time
	Logger     *slog.Logger
	Notifier   Notifier
	Auditor    Auditor
	RequestTTL time.Duration // how long a pending request lives (default 15m)
	GrantTTL   time.Duration // how long an unclaimed grant lives (default 5m)
	MaxPending int           // cap on concurrent pending requests (default 1000)
}

// Broker is the in-memory approval state machine. Safe for concurrent use.
type Broker struct {
	clock      func() time.Time
	logger     *slog.Logger
	notifier   Notifier
	auditor    Auditor
	requestTTL time.Duration
	grantTTL   time.Duration
	maxPending int

	mu      sync.Mutex
	byID    map[string]*request
	byKey   map[string]*request // subjectKey\x00sqlHash → pending request
	grants  map[string]grant    // subjectKey\x00sqlHash → grant
	entropy *ulid.MonotonicEntropy
}

// New builds a Broker with sensible defaults.
func New(o Options) *Broker {
	b := &Broker{
		clock:      o.Clock,
		logger:     o.Logger,
		notifier:   o.Notifier,
		auditor:    o.Auditor,
		requestTTL: o.RequestTTL,
		grantTTL:   o.GrantTTL,
		maxPending: o.MaxPending,
		byID:       map[string]*request{},
		byKey:      map[string]*request{},
		grants:     map[string]grant{},
	}
	if b.clock == nil {
		b.clock = time.Now
	}
	if b.logger == nil {
		b.logger = slog.Default()
	}
	if b.requestTTL <= 0 {
		b.requestTTL = 15 * time.Minute
	}
	if b.grantTTL <= 0 {
		b.grantTTL = 5 * time.Minute
	}
	if b.maxPending <= 0 {
		b.maxPending = 1000
	}
	registerMetrics()
	return b
}

// keyOf joins the dedupe/grant key.
func keyOf(subjectKey, sqlHash string) string { return subjectKey + "\x00" + sqlHash }

// Require registers (or dedupes) an approval request. When an existing
// pending request already covers the same subject + SQL hash, its Ticket
// is returned with created=false and no second webhook fires. A new
// request past MaxPending returns ErrBrokerFull (fail-closed).
func (b *Broker) Require(_ context.Context, in RequireInput) (Ticket, bool, error) {
	b.mu.Lock()
	now := b.clock()
	b.expireLocked(now)

	key := keyOf(in.SubjectKey, in.SQLHash)
	if existing, ok := b.byKey[key]; ok && existing.state == StatePending {
		t := Ticket{ID: existing.id, State: existing.state, ExpiresAt: existing.expiresAt}
		b.mu.Unlock()
		return t, false, nil
	}

	if b.pendingCountLocked() >= b.maxPending {
		b.mu.Unlock()
		return Ticket{}, false, ErrBrokerFull
	}

	r := &request{
		id:          b.newID(now),
		subjectKey:  in.SubjectKey,
		subject:     in.Subject,
		sqlHash:     in.SQLHash,
		sqlSample:   in.SQLSample,
		reasons:     in.Reasons,
		policies:    in.Policies,
		acceptToken: randomToken(),
		rejectToken: randomToken(),
		state:       StatePending,
		createdAt:   now,
		expiresAt:   now.Add(b.requestTTL),
		done:        make(chan struct{}),
	}
	b.byID[r.id] = r
	b.byKey[key] = r
	view := r.view()
	accept, reject := r.acceptToken, r.rejectToken
	mPending.Inc()
	b.mu.Unlock()

	// Fire webhook + audit outside the lock.
	if b.notifier != nil {
		b.notifier.Notify(view, capabilityToken(accept), capabilityToken(reject))
	}
	if b.auditor != nil {
		b.auditor.ApprovalEvent("requested", view, nil)
	}
	return Ticket{ID: r.id, State: StatePending, ExpiresAt: r.expiresAt}, true, nil
}

// Wait blocks until the request identified by id is decided or expires,
// the max duration elapses, or ctx is cancelled. It returns the current
// state; a still-pending state means the sync wait timed out.
func (b *Broker) Wait(ctx context.Context, id string, maxWait time.Duration) (State, error) {
	b.mu.Lock()
	r, ok := b.byID[id]
	if !ok {
		b.mu.Unlock()
		return "", ErrNotFound
	}
	if r.state != StatePending {
		st := r.state
		b.mu.Unlock()
		return st, nil
	}
	done := r.done
	b.mu.Unlock()

	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	case <-ctx.Done():
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.expireLocked(b.clock())
	if cur, ok := b.byID[id]; ok {
		return cur.state, nil
	}
	return StateExpired, nil
}

// Accept marks the request approved and mints a single-use grant. It is
// idempotent for a repeat accept and returns ErrAlreadyDecided when the
// request was already rejected.
func (b *Broker) Accept(id, token, remoteAddr string) (DecisionResult, error) {
	return b.decide(id, token, true, remoteAddr)
}

// Reject marks the request rejected.
func (b *Broker) Reject(id, token, remoteAddr string) (DecisionResult, error) {
	return b.decide(id, token, false, remoteAddr)
}

func (b *Broker) decide(id, token string, accept bool, remoteAddr string) (DecisionResult, error) {
	b.mu.Lock()
	now := b.clock()
	b.expireLocked(now)
	r, ok := b.byID[id]
	if !ok {
		b.mu.Unlock()
		return DecisionResult{}, ErrNotFound
	}
	want := r.rejectToken
	if accept {
		want = r.acceptToken
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(want)) != 1 {
		b.mu.Unlock()
		return DecisionResult{}, ErrTokenMismatch
	}

	// Idempotent same-verb; conflicting verb after decision is an error.
	targetState := StateRejected
	if accept {
		targetState = StateApproved
	}
	switch r.state {
	case StatePending:
		r.state = targetState
		r.decidedAt = now
		r.approverIP = remoteAddr
		close(r.done)
		if accept {
			b.grants[keyOf(r.subjectKey, r.sqlHash)] = grant{approvalID: r.id, expiresAt: now.Add(b.grantTTL)}
		}
		delete(b.byKey, keyOf(r.subjectKey, r.sqlHash))
		mPending.Dec()
		mDecisions.WithLabelValues(string(targetState)).Inc()
		mWaitSecs.Observe(now.Sub(r.createdAt).Seconds())
		view := r.view()
		b.mu.Unlock()
		if b.auditor != nil {
			evt := "rejected"
			if accept {
				evt = "approved"
			}
			b.auditor.ApprovalEvent(evt, view, map[string]any{
				"approver_remote_ip":  remoteAddr,
				"decision_latency_ms": now.Sub(r.createdAt).Milliseconds(),
			})
		}
		return DecisionResult{State: targetState}, nil
	case targetState, StateConsumed:
		b.mu.Unlock()
		return DecisionResult{State: r.state, AlreadyDecided: true}, nil
	default:
		st := r.state
		b.mu.Unlock()
		return DecisionResult{State: st, AlreadyDecided: true}, ErrAlreadyDecided
	}
}

// ConsumeGrant atomically claims the single-use grant for subjectKey +
// sqlHash, returning the approval id. A grant can be consumed exactly
// once.
func (b *Broker) ConsumeGrant(subjectKey, sqlHash string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clock()
	b.expireLocked(now)
	key := keyOf(subjectKey, sqlHash)
	g, ok := b.grants[key]
	if !ok || now.After(g.expiresAt) {
		delete(b.grants, key)
		return "", false
	}
	delete(b.grants, key)
	if r, ok := b.byID[g.approvalID]; ok {
		r.state = StateConsumed
	}
	return g.approvalID, true
}

// Get returns a token-free view of the request.
func (b *Broker) Get(id string) (View, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.expireLocked(b.clock())
	r, ok := b.byID[id]
	if !ok {
		return View{}, false
	}
	return r.view(), true
}

// Pending returns views of every pending request (admin surface).
func (b *Broker) Pending() []View {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.expireLocked(b.clock())
	var out []View
	for _, r := range b.byID {
		if r.state == StatePending {
			out = append(out, r.view())
		}
	}
	return out
}

// Run drives the janitor: it expires stale requests/grants and GCs decided
// records. It returns when ctx is cancelled.
func (b *Broker) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.mu.Lock()
			expired := b.expireLocked(b.clock())
			b.gcLocked(b.clock())
			b.mu.Unlock()
			if b.auditor != nil {
				for _, v := range expired {
					b.auditor.ApprovalEvent("expired", v, nil)
				}
			}
		}
	}
}

// SubjectKeyOf returns a View's owning subject key (for status-poll authz),
// derived from the public Subject fields to match the queryservice key.
func SubjectKeyOf(v View) string { return v.Subject.Issuer + "\x00" + v.Subject.ID }

// --- lock-held helpers ---

func (b *Broker) pendingCountLocked() int {
	n := 0
	for _, r := range b.byID {
		if r.state == StatePending {
			n++
		}
	}
	return n
}

// expireLocked transitions timed-out pending requests to expired and drops
// expired grants. It returns the views of newly-expired requests so the
// caller can audit them after releasing the lock (never calls the auditor
// while holding the mutex).
func (b *Broker) expireLocked(now time.Time) []View {
	var expired []View
	for _, r := range b.byID {
		if r.state == StatePending && now.After(r.expiresAt) {
			r.state = StateExpired
			r.decidedAt = now
			close(r.done)
			delete(b.byKey, keyOf(r.subjectKey, r.sqlHash))
			mPending.Dec()
			mDecisions.WithLabelValues(string(StateExpired)).Inc()
			expired = append(expired, r.view())
		}
	}
	for k, g := range b.grants {
		if now.After(g.expiresAt) {
			delete(b.grants, k)
		}
	}
	return expired
}

// gcLocked removes decided/expired records older than the request TTL so
// status polls can still find a just-decided request but memory is bounded.
func (b *Broker) gcLocked(now time.Time) {
	for id, r := range b.byID {
		if r.state == StatePending {
			continue
		}
		if now.Sub(r.decidedAt) > b.requestTTL {
			delete(b.byID, id)
		}
	}
}

func (b *Broker) newID(now time.Time) string {
	if b.entropy == nil {
		b.entropy = ulid.Monotonic(rand.Reader, 0)
	}
	return ulid.MustNew(ulid.Timestamp(now), b.entropy).String()
}

func randomToken() string {
	var buf [32]byte
	_, _ = rand.Read(buf[:])
	return base64.RawURLEncoding.EncodeToString(buf[:])
}

// capabilityToken is the token as it appears in the accept/reject URL.
func capabilityToken(tok string) string { return tok }
