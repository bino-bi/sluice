// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice

import (
	"context"
	"log/slog"
	"time"

	"github.com/bino-bi/sluice/internal/approval"
	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/policycache"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/internal/schema"
	pkgapi "github.com/bino-bi/sluice/pkg/apitypes"
	pkgmask "github.com/bino-bi/sluice/pkg/mask"
)

// Options wires the service together. Parser, Policy, Rewriter, Executor,
// and Audit are required; Schema is optional (rewriter handles nil).
type Options struct {
	Parser   parser.Parser
	Policy   policyEvaluator
	Rewriter rewriterRewrite
	Executor executorRun
	Audit    auditEmitter
	Schema   schema.Cache

	Clock  func() time.Time
	Logger *slog.Logger

	Limits Limits

	// RateLimiter, when set, gates each request against the caller's
	// per-subject rate limit before any work is done. Nil disables per-
	// subject rate limiting (the global concurrency gate still applies).
	RateLimiter rateLimiter

	// AuditBestEffort relaxes the fail-closed audit posture. When false
	// (the default), a query is not served unless its access audit record
	// is durably enqueued — if the audit dispatcher cannot accept the
	// record, Execute returns ERR_AUDIT_UNAVAILABLE and no rows are
	// returned. When true, an audit enqueue failure is logged and the query
	// proceeds anyway.
	AuditBestEffort bool

	// Masks resolves post-query mask providers (FPE, fake, jitter, hmac).
	// Nil falls back to mask.Default(). Keys and Salts resolve their key /
	// salt references from internal/secrets.
	Masks *pkgmask.Registry
	Keys  pkgmask.KeyStore
	Salts pkgmask.SaltStore

	// Cache memoises (Decision, RewriteResult) per SQL text + identity
	// under the active snapshot. Nil disables caching (the default). Rate
	// limiting, budgets, approval, and audit still run per request.
	Cache rewriteCache

	// Approvals gates queries that policy marked as requiring human
	// approval. Nil means no ApprovalPolicy can be honoured — a decision
	// carrying an approval requirement then fails closed with an internal
	// error. ApprovalSyncWait bounds the in-request wait before returning
	// ERR_APPROVAL_PENDING.
	Approvals        approvalBroker
	ApprovalSyncWait time.Duration

	// Budget, when set, enforces per-subject daily budgets: Check runs
	// pre-admission and Record accumulates usage after execution.
	Budget budgetGate
}

// Limits controls request-level bounds the service enforces before it
// dispatches into lower layers.
type Limits struct {
	// DefaultMaxRows is applied when QueryRequest.MaxRows is zero.
	DefaultMaxRows int64

	// MaxRowsCeiling is the hard upper bound on MaxRows, regardless of the
	// caller-supplied value. Zero falls back to DefaultMaxRows so that, by
	// default, no caller (including an untrusted agent) can request more
	// than the default row count.
	MaxRowsCeiling int64

	// DefaultTimeout is applied when QueryRequest.Timeout is zero.
	DefaultTimeout time.Duration

	// MaxTimeout is the hard upper bound on the per-request Timeout. Zero
	// falls back to DefaultTimeout.
	MaxTimeout time.Duration

	// MaxSQLBytes rejects inputs larger than this before parsing. Zero
	// falls back to parser.DefaultMaxSQLBytes.
	MaxSQLBytes int

	// MaxConcurrent caps parallel in-flight Execute calls. Zero disables
	// the semaphore (unbounded).
	MaxConcurrent int

	// SQLSampleBytes copies the leading bytes of every request SQL into
	// the audit record. Zero means do not store any sample (privacy
	// default).
	SQLSampleBytes int

	// DisableCrossCatalog rejects any query whose parsed table set spans
	// more than one catalog (ACL_REJECTED), before policy evaluation and
	// before the rewrite cache. Only three-part (catalog.schema.table)
	// references carry a catalog — parity with the policy-rule
	// equivalent size(query.catalogs) > 1.
	DisableCrossCatalog bool
}

// Service is the orchestrator. One instance is shared across all
// transports.
type Service struct {
	opts Options
	sem  chan struct{}
}

// New builds a Service. Panics on missing required dependencies — a
// misconfigured Service is always a programmer error, never a runtime
// condition.
func New(opts Options) *Service {
	if opts.Parser == nil {
		panic("queryservice: Parser required")
	}
	if opts.Policy == nil {
		panic("queryservice: Policy required")
	}
	if opts.Rewriter == nil {
		panic("queryservice: Rewriter required")
	}
	if opts.Executor == nil {
		panic("queryservice: Executor required")
	}
	if opts.Audit == nil {
		panic("queryservice: Audit required")
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Limits.DefaultTimeout <= 0 {
		opts.Limits.DefaultTimeout = 30 * time.Second
	}
	if opts.Limits.DefaultMaxRows <= 0 {
		opts.Limits.DefaultMaxRows = 100_000
	}
	if opts.Limits.MaxRowsCeiling <= 0 {
		opts.Limits.MaxRowsCeiling = opts.Limits.DefaultMaxRows
	}
	if opts.Limits.MaxTimeout <= 0 {
		opts.Limits.MaxTimeout = opts.Limits.DefaultTimeout
	}
	if opts.Limits.MaxSQLBytes <= 0 {
		opts.Limits.MaxSQLBytes = parser.DefaultMaxSQLBytes
	}
	s := &Service{opts: opts}
	if opts.Limits.MaxConcurrent > 0 {
		s.sem = make(chan struct{}, opts.Limits.MaxConcurrent)
	}
	return s
}

// Origin identifies which transport initiated a request.
type Origin string

// Origin values.
const (
	OriginREST  Origin = "rest"
	OriginMCP   Origin = "mcp"
	OriginAdmin Origin = "admin"
)

// QueryRequest is the decoded request passed by transports.
type QueryRequest struct {
	SQL      string
	Params   []any
	MaxRows  int64
	Timeout  time.Duration
	Format   executor.OutputFormat
	User     *identity.UserCtx
	Origin   Origin
	Facts    *policy.RequestFacts
	Metadata map[string]string
}

// QueryResult is the output of Execute. Rows is an iterator the caller
// must Close; closing emits the success-path audit record.
type QueryResult struct {
	QueryID    string
	Columns    []executor.ColumnInfo
	Rows       executor.RowIterator
	RowCount   *int64
	Truncated  bool
	Rewrites   []string
	Applied    []pkgapi.AppliedPolicy
	Decision   string
	DurationMs int64
}

// Dependency interfaces. Using narrow interfaces at this boundary lets
// the queryservice tests inject fakes without importing cgo-heavy
// implementations (DuckDB, pg_query).

// policyEvaluator wraps the subset of *policy.Engine we need.
type policyEvaluator interface {
	Evaluate(ctx context.Context, in policy.Input) (*policy.Decision, error)
	Explain(ctx context.Context, in policy.Input) (*pkgapi.ExplainResult, error)
}

// snapshotInfoer is implemented by *policy.Engine and lets the cache build
// a sound key. A policyEvaluator that does not implement it (e.g. a test
// fake) simply disables caching.
type snapshotInfoer interface {
	SnapshotInfo() (version int64, digest string, keyHeaders []string, allHeaders bool)
}

// rewriteCache is the subset of *policycache.Cache the service consumes.
type rewriteCache interface {
	Get(k policycache.Key) (*policycache.Entry, bool)
	Put(k policycache.Key, e *policycache.Entry)
}

// approvalBroker is the subset of *approval.Broker the service consumes.
type approvalBroker interface {
	Require(ctx context.Context, in approval.RequireInput) (approval.Ticket, bool, error)
	Wait(ctx context.Context, id string, maxWait time.Duration) (approval.State, error)
	ConsumeGrant(subjectKey, sqlHash string) (string, bool)
	Get(id string) (approval.View, bool)
}

// rewriterRewrite wraps the subset of *rewriter.Rewriter we need.
type rewriterRewrite interface {
	Rewrite(ctx context.Context, req rewriter.RewriteRequest) (*rewriter.RewriteResult, error)
}

// executorRun wraps the subset of *executor.Executor we need.
type executorRun interface {
	Execute(ctx context.Context, req executor.Request) (*executor.Response, error)
}

// auditEmitter wraps the subset of *audit.Dispatcher we need.
type auditEmitter interface {
	Enqueue(ctx context.Context, r *audit.Record) error
}

// rateLimiter wraps the subset of *ratelimit.Limiter we need.
type rateLimiter interface {
	Allow(subject, issuer string) bool
}

// budgetGate wraps the subset of *budget.Manager we need.
type budgetGate interface {
	Check(ctx context.Context, subject, issuer string) error
	Record(subject, issuer string, cpu time.Duration, rows int64)
}
