// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"math"
	"strconv"

	pg "github.com/pganalyze/pg_query_go/v6"
)

// The row cap is an outer LIMIT of RewriteRequest.RowCap. DuckDB builds the
// whole result before the first row is read, so without it a SELECT on a
// large table computes every row only for the row limit to drop most. It
// adds no note to Rewrites: the result the client gets is the same.

// rowCapOf returns the cap of req, or 0 when none applies. A sample gets
// none: a LIMIT inside the sample would draw it from the first rows only.
func rowCapOf(req RewriteRequest) int64 {
	if req.RowCap <= 0 || req.RowCap > math.MaxInt32 || req.Sample != nil {
		return 0
	}
	if eff := req.Decision.Rewrite; eff != nil && eff.Sample != nil {
		return 0
	}
	return req.RowCap
}

// capLimit lowers the LIMIT of sel to n. A constant LIMIT from 0 to n
// stays. Anything else (none, a larger or negative constant, a parameter,
// an expression) becomes n, except that keepExpr keeps an expression: its
// value can be smaller than n. It reports whether sel changed.
func capLimit(sel *pg.SelectStmt, n int64, keepExpr bool) bool {
	if lc := sel.LimitCount; lc != nil {
		c := lc.GetAConst()
		if c == nil && keepExpr {
			return false
		}
		if iv := c.GetIval(); iv != nil && iv.Ival >= 0 && int64(iv.Ival) <= n {
			return false
		}
	}
	sel.LimitCount = intConst(n)
	sel.LimitOption = pg.LimitOption_LIMIT_OPTION_COUNT
	return true
}

// capPassThrough adds the row cap to the text of a SELECT that needs no
// other change, so DuckDB still runs the client's own text: a SELECT
// without LIMIT and OFFSET gets "LIMIT n" at its end, and a larger constant
// LIMIT is replaced in place. Any other form is returned as it is. The
// result is parsed again, and a text that does not parse to one SELECT
// with LIMIT n is dropped.
func capPassThrough(raw string, tree *pg.ParseResult, n int64) string {
	if tree == nil || len(tree.Stmts) != 1 {
		return raw
	}
	rs := tree.Stmts[0]
	sel := rs.GetStmt().GetSelectStmt()
	if sel == nil || sel.IntoClause != nil || len(sel.LockingClause) > 0 {
		return raw
	}
	num := strconv.FormatInt(n, 10)
	var out string
	switch {
	case sel.LimitCount == nil && sel.LimitOffset == nil:
		start := int(rs.GetStmtLocation())
		end := len(raw)
		if l := int(rs.GetStmtLen()); l > 0 {
			end = start + l
		}
		if start < 0 || end > len(raw) || start > end {
			return raw
		}
		// The line break ends a trailing "--" comment.
		out = raw[:end] + "\nLIMIT " + num + raw[end:]
	default:
		c := sel.LimitCount.GetAConst()
		iv := c.GetIval()
		if iv == nil || iv.Ival < 0 || int64(iv.Ival) <= n {
			return raw
		}
		loc := int(c.GetLocation())
		end := loc
		for end < len(raw) && (raw[end] >= '0' && raw[end] <= '9' || raw[end] == '_') {
			end++
		}
		if loc < 0 || end == loc {
			return raw
		}
		out = raw[:loc] + num + raw[end:]
	}
	check, err := pg.Parse(out)
	if err != nil || len(check.Stmts) != 1 {
		return raw
	}
	got := check.Stmts[0].GetStmt().GetSelectStmt()
	if got == nil || got.LimitCount.GetAConst().GetIval().GetIval() != int32(n) {
		return raw
	}
	return out
}
