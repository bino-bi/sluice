// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"context"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// Engine names.
const (
	EngineNameYAML = "yaml"
)

// PolicyEngine is the pluggable policy-decision contract. The built-in
// YAML *Engine implements it; v2 adds OPA and ReBAC engines and a
// Composite that fans out across several. queryservice consumes the
// narrower policyEvaluator, so any PolicyEngine satisfies it structurally.
type PolicyEngine interface {
	Evaluate(ctx context.Context, in Input) (*Decision, error)
	Explain(ctx context.Context, in Input) (*apitypes.ExplainResult, error)
	ApplySnapshot(ctx context.Context, src *config.Snapshot) error
	// Name identifies the engine for audit and metrics.
	Name() string
}

// Name reports the built-in YAML engine's name.
func (e *Engine) Name() string { return EngineNameYAML }

var _ PolicyEngine = (*Engine)(nil)
