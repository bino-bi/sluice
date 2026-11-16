// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"math"
	"strconv"

	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/policy"
)

// applyRewrite applies the folded QueryRewritePolicy effect and the row
// cap to the AST. Only the LIMIT instruction mutates the tree here;
// sampling wraps the deparsed SQL afterwards (pg_query's grammar cannot
// represent DuckDB's USING SAMPLE clause) and the timeout override is
// enforced by queryservice, not the SQL.
func (s *state) applyRewrite(res *pg.ParseResult) error {
	var limitMax int64
	if eff := s.decision.Rewrite; eff != nil {
		limitMax = eff.LimitMax
	}
	if (limitMax <= 0 && s.rowCap <= 0) || len(res.Stmts) == 0 {
		return nil
	}
	// A LIMIT inside would draw the sample from the first rows only. The
	// MaxRows clamp in queryservice still caps the sample's output.
	if s.sample != nil {
		if limitMax > 0 {
			s.rewrites = append(s.rewrites, "limit-deferred:sample")
		}
		return nil
	}
	sel := res.Stmts[0].Stmt.GetSelectStmt()
	if sel == nil {
		// EXPLAIN carries no row set to bound; queryservice's MaxRows clamp
		// still applies to whatever it emits.
		if limitMax > 0 {
			s.rewrites = append(s.rewrites, "limit-skipped:not-select")
		}
		return nil
	}
	if limitMax > 0 {
		limitStr := strconv.FormatInt(limitMax, 10)
		hadLimit := sel.LimitCount != nil
		changed := capLimit(sel, limitMax, false)
		if changed && limitMax < math.MaxInt32 {
			// One row over the limit lets the row iterator see that the
			// result was cut. It still stops at limitMax.
			sel.LimitCount = intConst(limitMax + 1)
		}
		switch {
		case !changed:
		case hadLimit:
			s.rewrites = append(s.rewrites, "limit-clamped:"+limitStr)
		default:
			s.rewrites = append(s.rewrites, "limit-injected:"+limitStr)
		}
	}
	if s.rowCap > 0 {
		capLimit(sel, s.rowCap, true)
	}
	return nil
}

// sampleWrap wraps a deparsed SELECT in a DuckDB USING SAMPLE subquery.
// Every token is compile-validated (rate is a float in (0,1], method is a
// whitelisted enum) — no user-controlled text enters the wrap.
func sampleWrap(sql string, sm *policy.CompiledSample) (string, string) {
	pct := strconv.FormatFloat(sm.Rate*100, 'f', -1, 64)
	clause := pct + "% (" + string(sm.Method) + ")"
	return "SELECT * FROM (" + sql + ") AS sluice_sample USING SAMPLE " + clause,
		"sample:" + string(sm.Method) + ":" + pct + "%"
}
