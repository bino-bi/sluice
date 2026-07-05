// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"strconv"

	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/policy"
)

// applyRewrite applies the folded QueryRewritePolicy effect to the AST.
// Only the LIMIT instruction mutates the tree here; sampling wraps the
// deparsed SQL afterwards (pg_query's grammar cannot represent DuckDB's
// USING SAMPLE clause) and the timeout override is enforced by
// queryservice, not the SQL.
func (s *state) applyRewrite(res *pg.ParseResult) error {
	eff := s.decision.Rewrite
	if eff == nil || eff.LimitMax <= 0 || len(res.Stmts) == 0 {
		return nil
	}
	sel := res.Stmts[0].Stmt.GetSelectStmt()
	if sel == nil {
		// Non-SELECT read-only statements (EXPLAIN / SET / SHOW / PRAGMA)
		// carry no row set to bound; queryservice's MaxRows clamp still
		// applies to whatever they emit.
		s.rewrites = append(s.rewrites, "limit-skipped:not-select")
		return nil
	}

	limitStr := strconv.FormatInt(eff.LimitMax, 10)
	if sel.LimitCount == nil {
		sel.LimitCount = intConst(eff.LimitMax)
		sel.LimitOption = pg.LimitOption_LIMIT_OPTION_COUNT
		s.rewrites = append(s.rewrites, "limit-injected:"+limitStr)
		return nil
	}
	// Keep an existing constant LIMIT that is already at or below the cap;
	// anything else (larger constant, parameter, expression) is clamped.
	if c := sel.LimitCount.GetAConst(); c != nil {
		if iv := c.GetIval(); iv != nil && int64(iv.Ival) <= eff.LimitMax {
			return nil
		}
	}
	sel.LimitCount = intConst(eff.LimitMax)
	sel.LimitOption = pg.LimitOption_LIMIT_OPTION_COUNT
	s.rewrites = append(s.rewrites, "limit-clamped:"+limitStr)
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
