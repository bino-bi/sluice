// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import "errors"

// Sentinel errors surfaced by the engine. Callers wrap these through the
// pkg/errors layer when rendering to the client so no internal message
// reaches the wire verbatim.
var (
	// ErrDeny indicates an SqlAccessPolicy with effect=deny matched.
	ErrDeny = errors.New("policy: access denied")
	// ErrReject indicates a QueryRejectPolicy rule fired.
	ErrReject = errors.New("policy: query rejected")
	// ErrSnapshotInvalid indicates ApplySnapshot refused the incoming
	// snapshot — the previous one stays live.
	ErrSnapshotInvalid = errors.New("policy: snapshot invalid")
	// ErrConditionUnsupported marks a CEL condition in MVP. The engine
	// accepts empty Conditions only; non-empty fails at compile time.
	ErrConditionUnsupported = errors.New("policy: CEL conditions not supported in MVP")
	// ErrRejectExprUnsupported marks a reject-rule expression in MVP.
	ErrRejectExprUnsupported = errors.New("policy: reject rule expressions not supported in MVP")
	// ErrTemplateInvalid marks a template string that does not parse.
	ErrTemplateInvalid = errors.New("policy: template invalid")
	// ErrTemplateVarMissing is returned at render time when a referenced
	// variable has no value on the current UserCtx / RequestFacts.
	ErrTemplateVarMissing = errors.New("policy: template variable missing")
)
