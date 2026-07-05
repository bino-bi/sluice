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
	// ErrConditionInvalid marks a CEL condition or reject expression that
	// fails to parse, type-check as bool, or plan.
	ErrConditionInvalid = errors.New("policy: condition expression invalid")
	// ErrFilterExprInvalid marks a CEL row-filter expression outside the
	// SQL-translatable subset.
	ErrFilterExprInvalid = errors.New("policy: row-filter expression invalid")
	// ErrTemplateInvalid marks a template string that does not parse.
	ErrTemplateInvalid = errors.New("policy: template invalid")
	// ErrTemplateVarMissing is returned at render time when a referenced
	// variable has no value on the current UserCtx / RequestFacts.
	ErrTemplateVarMissing = errors.New("policy: template variable missing")
)
