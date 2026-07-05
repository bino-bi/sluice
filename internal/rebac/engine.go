// SPDX-License-Identifier: AGPL-3.0-or-later

package rebac

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	lru "github.com/hashicorp/golang-lru/v2/expirable"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// secretResolver resolves a secret:// token reference to raw bytes.
type secretResolver interface {
	Resolve(ctx context.Context, uri string) ([]byte, error)
}

// Options configures the ReBAC engine.
type Options struct {
	Secrets   secretResolver
	CacheTTL  time.Duration
	CacheSize int
	Clock     func() time.Time
	Logger    *slog.Logger
	// NewChecker builds a RelationChecker for a backend. Defaults to the
	// OpenFGA client; tests inject a Fake.
	NewChecker func(b apitypes.RelationshipBackend, token []byte) RelationChecker
}

// Engine evaluates RelationshipPolicies as a composite member.
type Engine struct {
	opts     Options
	compiled atomic.Pointer[[]*compiledRel]
	cache    *lru.LRU[string, bool]
}

type compiledRel struct {
	name        string
	match       policy.CompiledSelector
	exclude     *policy.CompiledSelector
	enforcement apitypes.EnforcementMode
	checks      []apitypes.RelationCheck
	checker     RelationChecker
}

// New builds the engine.
func New(opts Options) *Engine {
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.CacheTTL <= 0 {
		opts.CacheTTL = 10 * time.Second
	}
	if opts.CacheSize <= 0 {
		opts.CacheSize = 10000
	}
	if opts.NewChecker == nil {
		opts.NewChecker = func(b apitypes.RelationshipBackend, token []byte) RelationChecker {
			return NewOpenFGAClient(ClientOptions{
				Endpoint: b.Endpoint, StoreID: b.StoreID, ModelID: b.AuthorizationModelID,
				Token: token, Timeout: time.Duration(b.Timeout),
			})
		}
	}
	return &Engine{opts: opts, cache: lru.NewLRU[string, bool](opts.CacheSize, nil, opts.CacheTTL)}
}

// Name implements policy.PolicyEngine.
func (e *Engine) Name() string { return "rebac" }

// ApplySnapshot compiles every RelationshipPolicy in the snapshot.
func (e *Engine) ApplySnapshot(ctx context.Context, src *config.Snapshot) error {
	if src == nil {
		empty := []*compiledRel{}
		e.compiled.Store(&empty)
		return nil
	}
	out := make([]*compiledRel, 0, len(src.RelationshipPolicies))
	for _, rp := range src.RelationshipPolicies {
		m, err := policy.CompileSelectorSpec(rp.Spec.Match)
		if err != nil {
			return fmt.Errorf("rebac: %s: spec.match: %w", rp.Metadata.Name, err)
		}
		cr := &compiledRel{
			name:        rp.Metadata.Name,
			match:       m,
			enforcement: rp.Spec.EnforcementMode,
			checks:      rp.Spec.Checks,
		}
		if rp.Spec.Exclude != nil {
			ex, err := policy.CompileSelectorSpec(*rp.Spec.Exclude)
			if err != nil {
				return fmt.Errorf("rebac: %s: spec.exclude: %w", rp.Metadata.Name, err)
			}
			cr.exclude = &ex
		}
		var token []byte
		if rp.Spec.Backend.TokenRef != "" && e.opts.Secrets != nil {
			token, err = e.opts.Secrets.Resolve(ctx, rp.Spec.Backend.TokenRef)
			if err != nil {
				return fmt.Errorf("rebac: %s: resolve tokenRef: %w", rp.Metadata.Name, err)
			}
		}
		cr.checker = e.opts.NewChecker(rp.Spec.Backend, token)
		out = append(out, cr)
	}
	e.compiled.Store(&out)
	e.cache.Purge()
	return nil
}

// Evaluate contributes allow/deny/abstain per the ReBAC checks. All checks
// true → allow; any false → explicit deny; backend error → error
// (fail-closed); no policy matched → abstain (composite-safe).
func (e *Engine) Evaluate(ctx context.Context, in policy.Input) (*policy.Decision, error) {
	ptr := e.compiled.Load()
	if ptr == nil || len(*ptr) == 0 {
		return abstain(), nil
	}
	mc := policy.MatchContext{User: in.User, Tables: in.Tables}
	matchedAny := false
	dec := &policy.Decision{Outcome: policy.OutcomeAllow, RowFilters: map[string]*policy.CompiledFilter{}, ColumnMasks: map[string]*policy.CompiledMask{}}

	for _, cr := range *ptr {
		tables := cr.match.MatchingTables(mc)
		if len(tables) == 0 {
			continue
		}
		shadow := cr.enforcement == apitypes.EnforcementAudit || cr.enforcement == apitypes.EnforcementDryRun
		ap := apitypes.AppliedPolicy{Kind: apitypes.KindRelationshipPolicy, Name: cr.name}
		for _, t := range tables {
			if cr.exclude != nil && cr.exclude.Match(policy.MatchContext{User: in.User, Tables: []parser.TableRef{t}}) {
				continue
			}
			matchedAny = true
			ok, err := e.checkTable(ctx, cr, in.User, t)
			if err != nil {
				return nil, err // fail-closed
			}
			if !ok {
				if shadow {
					dec.Shadow = append(dec.Shadow, ap)
					continue
				}
				return &policy.Decision{
					Outcome: policy.OutcomeDeny,
					DenyReason: &policy.DenyReason{
						PolicyName: cr.name,
						Message:    fmt.Sprintf("relationship check failed on %s", tableKey(t)),
						Code:       "ACL_DENIED",
					},
				}, nil
			}
			if shadow {
				dec.Shadow = append(dec.Shadow, ap)
			} else {
				dec.Applied = append(dec.Applied, ap)
			}
		}
	}
	if !matchedAny {
		return abstain(), nil
	}
	return dec, nil
}

// checkTable runs every check for one table; all must pass. Results are
// LRU-cached (positive and negative) for CacheTTL; errors are never cached.
func (e *Engine) checkTable(ctx context.Context, cr *compiledRel, user *identity.UserCtx, t parser.TableRef) (bool, error) {
	for _, chk := range cr.checks {
		object := renderTemplate(chk.ObjectTemplate, user, t)
		subjectTmpl := chk.SubjectTemplate
		if subjectTmpl == "" {
			subjectTmpl = "user:{{subject.id}}"
		}
		subject := renderTemplate(subjectTmpl, user, t)
		key := object + "#" + chk.Relation + "@" + subject
		if v, ok := e.cache.Get(key); ok {
			if !v {
				return false, nil
			}
			continue
		}
		allowed, err := cr.checker.Check(ctx, object, chk.Relation, subject)
		if err != nil {
			return false, fmt.Errorf("rebac: %s: %w", cr.name, err)
		}
		e.cache.Add(key, allowed)
		if !allowed {
			return false, nil
		}
	}
	return true, nil
}

// Explain wraps Evaluate.
func (e *Engine) Explain(ctx context.Context, in policy.Input) (*apitypes.ExplainResult, error) {
	dec, err := e.Evaluate(ctx, in)
	if err != nil {
		return nil, err
	}
	return &apitypes.ExplainResult{
		Matched:   dec.Applied,
		Shadow:    dec.Shadow,
		Effective: apitypes.EffectiveDecision{Decision: string(dec.Outcome)},
	}, nil
}

func abstain() *policy.Decision {
	return &policy.Decision{
		Outcome:    policy.OutcomeDeny,
		Abstained:  true,
		DenyReason: &policy.DenyReason{Message: "no RelationshipPolicy matched", Code: "ACL_DENIED"},
	}
}

func renderTemplate(tmpl string, user *identity.UserCtx, t parser.TableRef) string {
	r := strings.NewReplacer(
		"{{catalog}}", t.Catalog,
		"{{schema}}", t.Schema,
		"{{table}}", t.Table,
		"{{subject.id}}", subjectID(user),
		"{{subject.email}}", subjectEmail(user),
	)
	return r.Replace(tmpl)
}

func subjectID(u *identity.UserCtx) string {
	if u == nil {
		return "anonymous"
	}
	return u.Subject
}

func subjectEmail(u *identity.UserCtx) string {
	if u == nil {
		return ""
	}
	return u.Email
}

func tableKey(t parser.TableRef) string { return t.Catalog + "." + t.Schema + "." + t.Table }

var _ policy.PolicyEngine = (*Engine)(nil)
