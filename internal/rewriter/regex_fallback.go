// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"fmt"

	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// regexFallback handles the case where the parser failed but the caller
// still wants the rewriter to decide what to do. MVP policy — defined in
// concept §4.8:
//
//   - If the decision indicates any row filter, column mask, rejection,
//     or deny, the unparsable query is rejected with
//     CodeUnsupportedSyntaxOnProtectedTable.
//   - Otherwise it is passed through unchanged.
//
// The regex table extractor lives in internal/parser; the queryservice
// calls it to populate the decision before handing the request here.
func (r *Rewriter) regexFallback(req RewriteRequest) (*RewriteResult, error) {
	if needsMutation(req.Decision) {
		return nil, pkgerr.New(pkgerr.CodeUnsupportedSyntax).
			WithMessage("query not parseable and one or more policies require a rewrite")
	}
	if len(req.Decision.Rejections) > 0 {
		return nil, fmt.Errorf("%w: policy rejected unparseable query", ErrUnsupportedSyntax)
	}
	return &RewriteResult{SQL: req.Raw, Changed: false}, nil
}
