// SPDX-License-Identifier: AGPL-3.0-or-later

// Package opaengine is an embedded Open Policy Agent (rego) policy engine.
// It implements policy.PolicyEngine so it can run standalone
// (policies.engine: opa) or as a composite member alongside the YAML
// engine. Rego decides allow/deny (and optionally row filters, column
// masks, rejections); the output is decoded through the YAML engine's
// proven compile paths so no rego author renders SQL text.
package opaengine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/open-policy-agent/opa/v1/rego"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// Options configures the engine.
type Options struct {
	ModuleDir string
	Query     string // default "data.sluice.main"
	Clock     func() time.Time
	Logger    *slog.Logger
}

// Engine evaluates rego modules against a request-derived input document.
type Engine struct {
	moduleDir string
	query     string
	clock     func() time.Time
	logger    *slog.Logger
	prepared  atomic.Pointer[rego.PreparedEvalQuery]
}

// New builds the engine and loads the modules once (if a dir is set).
func New(opts Options) (*Engine, error) {
	e := &Engine{
		moduleDir: opts.ModuleDir,
		query:     opts.Query,
		clock:     opts.Clock,
		logger:    opts.Logger,
	}
	if e.query == "" {
		e.query = "data.sluice.main"
	}
	if e.clock == nil {
		e.clock = time.Now
	}
	if e.logger == nil {
		e.logger = slog.Default()
	}
	return e, nil
}

// Name implements policy.PolicyEngine.
func (e *Engine) Name() string { return "opa" }

// Prepare compiles the rego modules. The snapshot is not used (rego reads
// its own modules from ModuleDir); it is accepted so that a config reload
// also loads the modules again. A module that does not compile is a
// policy.ErrSnapshotInvalid, and the old modules stay active.
func (e *Engine) Prepare(ctx context.Context, _ *config.Snapshot) (func(), error) {
	if e.moduleDir == "" {
		return nil, fmt.Errorf("%w: opaengine: moduleDir is required", policy.ErrSnapshotInvalid)
	}
	modules, err := loadModules(e.moduleDir)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", policy.ErrSnapshotInvalid, err)
	}
	if len(modules) == 0 {
		return nil, fmt.Errorf("%w: opaengine: no .rego modules found under %s", policy.ErrSnapshotInvalid, e.moduleDir)
	}
	opts := []func(*rego.Rego){rego.Query(e.query)}
	for name, src := range modules {
		opts = append(opts, rego.Module(name, src))
	}
	pq, err := rego.New(opts...).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: opaengine: prepare: %w", policy.ErrSnapshotInvalid, err)
	}
	return func() { e.prepared.Store(&pq) }, nil
}

// ApplySnapshot implements policy.PolicyEngine.
func (e *Engine) ApplySnapshot(ctx context.Context, src *config.Snapshot) error {
	apply, err := e.Prepare(ctx, src)
	if err != nil {
		return err
	}
	apply()
	return nil
}

// Evaluate builds the input document, runs the prepared query, and decodes
// the output contract into a policy.Decision. Any contract violation is an
// error (fail-closed) — never a silent allow.
func (e *Engine) Evaluate(ctx context.Context, in policy.Input) (*policy.Decision, error) {
	pq := e.prepared.Load()
	if pq == nil {
		return nil, fmt.Errorf("opaengine: no modules loaded (fail-closed)")
	}
	rs, err := pq.Eval(ctx, rego.EvalInput(buildInput(in)))
	if err != nil {
		return nil, fmt.Errorf("opaengine: eval: %w", err)
	}
	if len(rs) == 0 || len(rs[0].Expressions) == 0 {
		// Undefined result → no decision → fail-closed deny.
		return abstainDeny("opa: no decision (fail-closed)"), nil
	}
	raw, err := json.Marshal(rs[0].Expressions[0].Value)
	if err != nil {
		return nil, fmt.Errorf("opaengine: marshal result: %w", err)
	}
	return decodeDecision(raw, in.Tables)
}

// Explain wraps Evaluate into an ExplainResult.
func (e *Engine) Explain(ctx context.Context, in policy.Input) (*apitypes.ExplainResult, error) {
	dec, err := e.Evaluate(ctx, in)
	if err != nil {
		return nil, err
	}
	out := &apitypes.ExplainResult{
		Effective: apitypes.EffectiveDecision{Decision: string(dec.Outcome)},
	}
	for name := range dec.RowFilters {
		out.Effective.RowFilters = append(out.Effective.RowFilters, name)
	}
	out.Matched = append(out.Matched, apitypes.AppliedPolicy{Kind: "OpaModule", Name: "opa/" + e.query})
	return out, nil
}

func abstainDeny(msg string) *policy.Decision {
	return &policy.Decision{
		Outcome:   policy.OutcomeDeny,
		Abstained: false,
		DenyReason: &policy.DenyReason{
			Message: msg,
			Code:    "ACL_DENIED",
		},
	}
}

// loadModules reads every *.rego file under dir.
func loadModules(dir string) (map[string]string, error) {
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("opaengine: read module dir: %w", err)
	}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".rego") {
			continue
		}
		path := filepath.Join(dir, ent.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("opaengine: read %s: %w", path, err)
		}
		out[ent.Name()] = string(b)
	}
	return out, nil
}

var _ policy.PolicyEngine = (*Engine)(nil)
