// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/schema"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
	pkgmask "github.com/bino-bi/sluice/pkg/mask"
)

// Options configures a new Rewriter.
type Options struct {
	Parser         parser.Parser
	Schema         schema.Cache // may be nil when SELECT-* expansion is not needed
	Logger         *slog.Logger
	Clock          func() time.Time
	DefaultCatalog string
	// Masks resolves mask types beyond the null/constant fast paths.
	// Nil falls back to mask.Default().
	Masks *pkgmask.Registry
	// Salts resolves secret:// salt references for salted hash masks.
	Salts pkgmask.SaltStore
}

// Rewriter is the sole public surface of the package. It is safe for
// concurrent use; callers share a single instance.
type Rewriter struct {
	opts Options
}

// New returns a new Rewriter. Parser is required — everything else takes
// its zero value on nil.
func New(opts Options) *Rewriter {
	if opts.Parser == nil {
		panic("rewriter: Parser is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	return &Rewriter{opts: opts}
}

// RewriteRequest carries everything Rewrite needs. AST may be nil if the
// caller has the raw SQL but couldn't parse it — the rewriter attempts
// the pass-through / regex-fallback path.
type RewriteRequest struct {
	AST      parser.AST
	Decision *policy.Decision
	User     *identity.UserCtx
	Facts    *policy.RequestFacts
	Raw      string
}

// RewriteResult carries the rewritten SQL, positional parameters, and an
// ordered list of audit annotations describing the mutations applied.
type RewriteResult struct {
	SQL         string
	Params      []any
	Fingerprint string
	Changed     bool
	Rewrites    []string
	// PostMasks lists columns that must be masked in Go after execution
	// (FPE, fake, jitter, hmac). ColumnIndex is the zero-based position in
	// the result set; queryservice builds one mask per entry.
	PostMasks []PostMask
}

// PostMask describes one post-query masking instruction bound to a result
// column position.
type PostMask struct {
	ColumnIndex int
	TableKey    string
	Column      string
	Type        apitypes.MaskType
	Args        pkgmask.Args
	Policy      string
}

// Rewrite applies req.Decision to req.AST. The general flow is:
//
//  1. Reject unsupported statement kinds.
//  2. Deny / reject: return a typed APIError.
//  3. Fast-path: pass-through when no mutation is required.
//  4. Clone the AST, apply expand-star → inject-filter → substitute-mask.
//  5. Deparse, compute fingerprint, return the result.
//
// Steps 4+ are only reached when req.AST != nil. The nil-AST branch
// handles the regex fallback.
func (r *Rewriter) Rewrite(ctx context.Context, req RewriteRequest) (*RewriteResult, error) {
	if req.Decision == nil {
		return nil, fmt.Errorf("rewriter: Decision required")
	}

	// Step 1: deny and reject short-circuit.
	if req.Decision.Outcome == policy.OutcomeDeny {
		dr := req.Decision.DenyReason
		e := pkgerr.New(pkgerr.CodeACLDenied)
		if dr != nil {
			if dr.Message != "" {
				e = e.WithMessage(dr.Message)
			}
			if dr.PolicyName != "" {
				e = e.WithPolicy(dr.PolicyName)
			}
		}
		return nil, e
	}
	if req.Decision.Outcome == policy.OutcomeReject {
		code := pkgerr.CodeACLRejected
		msg := "query rejected by policy"
		policyName := ""
		if len(req.Decision.Rejections) > 0 {
			top := req.Decision.Rejections[0]
			if top.Code != "" {
				code = pkgerr.Code(top.Code)
			}
			if top.Message != "" {
				msg = top.Message
			}
			policyName = top.PolicyName
		}
		e := pkgerr.New(code).WithMessage(msg)
		if policyName != "" {
			e = e.WithPolicy(policyName)
		}
		return nil, e
	}

	// Step 2: statement-kind gate. AST == nil means the parser failed
	// upstream; that path is handled by the regex fallback.
	if req.AST != nil {
		if err := validateStatementKind(req.AST.Statement()); err != nil {
			return nil, err
		}
	}

	// Step 3: pass-through fast path.
	if !needsMutation(req.Decision) {
		if req.AST == nil {
			return r.regexFallback(req)
		}
		return &RewriteResult{
			SQL:         req.Raw,
			Fingerprint: req.AST.Fingerprint(),
			Changed:     false,
		}, nil
	}

	// Step 4: AST is required to apply mutations.
	if req.AST == nil {
		return nil, fmt.Errorf("%w: rewrite required but AST missing", ErrUnsupportedSyntax)
	}

	// Lift the pg_query AST so the mutators can operate on proto nodes.
	raw, ok := req.AST.Raw().(*pg.ParseResult)
	if !ok {
		return nil, ErrForeignAST
	}
	clone, ok := req.AST.Clone().Raw().(*pg.ParseResult)
	if !ok {
		return nil, ErrForeignAST
	}

	state := &state{
		ctx:            ctx,
		decision:       req.Decision,
		user:           req.User,
		facts:          req.Facts,
		schema:         r.opts.Schema,
		defaultCatalog: r.opts.DefaultCatalog,
		masks:          r.opts.Masks,
		salts:          r.opts.Salts,
	}

	if err := state.applyExpandStar(clone); err != nil {
		return nil, err
	}
	if err := state.applyInjectFilter(clone); err != nil {
		return nil, err
	}
	if err := state.applySubstituteMask(clone); err != nil {
		return nil, err
	}
	if err := state.applyRewrite(clone); err != nil {
		return nil, err
	}

	// Step 5: deparse.
	sql, err := pg.Deparse(clone)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDeparseFailed, err)
	}
	fp, ferr := pg.Fingerprint(sql)
	if ferr != nil {
		// Deparse succeeded so fingerprint almost never fails — keep the
		// original fingerprint as a fallback so audit remains populated.
		fp = req.AST.Fingerprint()
	}
	_ = raw // kept so the compiler retains the type assertion in early slices

	// Step 6: sampling wraps the deparsed text — pg_query cannot express
	// DuckDB's USING SAMPLE, so the wrap happens after deparse and the
	// fingerprint above is intentionally computed on the inner SQL (the
	// wrapped form no longer parses under the PG grammar).
	if eff := req.Decision.Rewrite; eff != nil && eff.Sample != nil {
		if req.AST.Statement() == parser.StmtSelect {
			var note string
			sql, note = sampleWrap(sql, eff.Sample)
			state.rewrites = append(state.rewrites, note)
		} else {
			state.rewrites = append(state.rewrites, "sample-skipped:"+string(req.AST.Statement()))
		}
	}

	return &RewriteResult{
		SQL:         sql,
		Params:      state.params,
		Fingerprint: fp,
		Changed:     true,
		Rewrites:    state.rewrites,
		PostMasks:   state.postMasks,
	}, nil
}

// state carries the per-Rewrite mutable bookkeeping.
type state struct {
	ctx            context.Context
	decision       *policy.Decision
	user           *identity.UserCtx
	facts          *policy.RequestFacts
	schema         schema.Cache
	defaultCatalog string
	masks          *pkgmask.Registry
	salts          pkgmask.SaltStore

	params    []any
	rewrites  []string
	postMasks []PostMask
}

// needsMutation reports whether the decision requires any AST change.
// Allow outcome with no row filters, masks, rewrites, or rejections
// means the SQL can be served verbatim. A timeout-only RewriteEffect is
// enforced by queryservice and keeps the pass-through fast path.
func needsMutation(d *policy.Decision) bool {
	if len(d.RowFilters) > 0 || len(d.ColumnMasks) > 0 {
		return true
	}
	return d.Rewrite != nil && (d.Rewrite.LimitMax > 0 || d.Rewrite.Sample != nil)
}

// validateStatementKind rejects statements that must never reach the
// executor. Write statements and DDL produce ErrStatementRejected with a
// CodeACLRejected APIError already wrapped; callers typically translate
// the sentinel into the specific client-facing error.
func validateStatementKind(k parser.StmtKind) error {
	if k.IsReadOnly() {
		return nil
	}
	switch k {
	case parser.StmtInsert, parser.StmtUpdate, parser.StmtDelete:
		return pkgerr.New(pkgerr.CodeACLRejected).
			WithMessage("write operations are not permitted")
	case parser.StmtCopy, parser.StmtDDL,
		parser.StmtAttach, parser.StmtLoad, parser.StmtInstall:
		return pkgerr.New(pkgerr.CodeUnsupportedSyntax).
			WithMessage(fmt.Sprintf("%s not supported", k))
	default:
		return pkgerr.New(pkgerr.CodeUnsupportedSyntax).
			WithMessage(fmt.Sprintf("%s not supported", k))
	}
}
