// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"time"

	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgmask "github.com/bino-bi/sluice/pkg/mask"
)

// Outcome is the top-level verdict of an evaluation.
type Outcome string

// Outcome values.
const (
	OutcomeAllow  Outcome = "allow"
	OutcomeDeny   Outcome = "deny"
	OutcomeReject Outcome = "reject"
)

// Decision is the output of Engine.Evaluate. It is consumed by the
// rewriter to produce the final SQL and by the audit package to record
// what was decided.
type Decision struct {
	Outcome     Outcome
	DenyReason  *DenyReason
	RowFilters  map[string]*CompiledFilter // key: catalog.schema.table
	ColumnMasks map[string]*CompiledMask   // key: catalog.schema.table.column
	Rejections  []Rejection
	Applied     []apitypes.AppliedPolicy
	Evaluated   int
	Duration    time.Duration
}

// DenyReason carries the policy that produced a deny outcome.
type DenyReason struct {
	PolicyName string
	Message    string
	Code       string
}

// CompiledFilter is the set of predicates a rewriter must AND/OR into a
// table reference's WHERE clause. Predicates are kept structured so the
// rewriter can emit parameterised SQL without string concatenation.
type CompiledFilter struct {
	TableKey  string
	Predicate *CompiledPredicate // top-level; may be nil for an empty filter
	Combine   apitypes.Combine
	Policies  []string
}

// CompiledMask is the resolved mask to apply to a column reference. The
// rewriter looks up Args and Type against pkgmask.Registry.Provider at
// substitution time.
type CompiledMask struct {
	TableKey string // catalog.schema.table
	Column   string
	Type     apitypes.MaskType
	Args     pkgmask.Args
	Policy   string
	Priority int32
}

// Rejection names a QueryRejectPolicy rule that fired.
type Rejection struct {
	PolicyName string
	RuleName   string
	Message    string
	Code       string
}
