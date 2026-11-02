// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"strconv"
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// MaxSampleSeed is the largest seed DuckDB's USING SAMPLE accepts.
const MaxSampleSeed = 2147483647

// MinSampleRate is the smallest rate the sample endpoint accepts.
const MinSampleRate = 1e-6

// SampleSpec is a sample request of the sample endpoint. queryservice
// builds it from validated input only.
type SampleSpec struct {
	Method       apitypes.SampleMethod
	Rows         int64   // > 0 in rows mode
	Rate         float64 // in [MinSampleRate, 1] in rate mode
	Seed         int64   // in [0, MaxSampleSeed]
	StratifyBy   []string
	MinGroupRows int64
}

const errSampleShape = "where must be one predicate on the sampled table"

// CheckSampleStatement refuses a statement that is not one SELECT of one
// table with at most a WHERE. A where text that closes its parenthesis can
// change the statement the sample endpoint built.
func CheckSampleStatement(res *pg.ParseResult) error {
	refuse := pkgerr.New(pkgerr.CodeUnsupportedSyntax).WithMessage(errSampleShape)
	if res == nil || len(res.Stmts) != 1 {
		return refuse
	}
	sel := res.Stmts[0].GetStmt().GetSelectStmt()
	if sel == nil || sel.Op != pg.SetOperation_SETOP_NONE {
		return refuse
	}
	if sel.WithClause != nil || len(sel.GroupClause) > 0 || sel.HavingClause != nil ||
		len(sel.SortClause) > 0 || sel.LimitCount != nil || sel.LimitOffset != nil ||
		len(sel.DistinctClause) > 0 || len(sel.WindowClause) > 0 || len(sel.LockingClause) > 0 ||
		sel.IntoClause != nil || len(sel.ValuesLists) > 0 {
		return refuse
	}
	if len(sel.FromClause) != 1 || sel.FromClause[0].GetRangeVar() == nil {
		return refuse
	}
	return nil
}

// checkSampleStratify refuses a stratified sample when a post-query mask
// applies. Its row hash would see the raw value, and the client picks the
// seed, so repeated calls could probe that value.
func (s *state) checkSampleStratify() error {
	if s.sample == nil || s.sample.Method != apitypes.SampleMethodStratified || len(s.postMasks) == 0 {
		return nil
	}
	return pkgerr.New(pkgerr.CodeMaskContext).WithMessage(
		"a stratified sample is not supported on a column with a post-query mask (" +
			s.postMasks[0].Column + "), name columns without it")
}

// endpointSampleWrap wraps a deparsed SELECT in the sample of the sample
// endpoint. Every token comes from a validated SampleSpec.
func endpointSampleWrap(sql string, sp *SampleSpec) (string, string) {
	seed := strconv.FormatInt(sp.Seed, 10)
	rows := strconv.FormatInt(sp.Rows, 10)
	pct := strconv.FormatFloat(sp.Rate*100, 'f', -1, 64)
	size := pct + "%"
	if sp.Rows > 0 {
		size = rows
	}
	note := "sample-endpoint:" + string(sp.Method) + ":" + size
	switch sp.Method {
	case apitypes.SampleMethodStratified:
		return stratifiedWrap(sql, sp, seed, rows), note
	case apitypes.SampleMethodReservoir:
		return "SELECT * FROM (" + sql + ") AS sluice_endpoint_sample USING SAMPLE " +
			rows + " ROWS (reservoir, " + seed + ")", note
	default:
		return "SELECT * FROM (" + sql + ") AS sluice_endpoint_sample USING SAMPLE " +
			pct + "% (" + string(sp.Method) + ", " + seed + ")", note
	}
}

// stratifiedWrap ranks the rows of each group by a seeded row hash and
// keeps the first ceil(n * share) of each group with at least
// MinGroupRows rows.
func stratifiedWrap(sql string, sp *SampleSpec, seed, rows string) string {
	groups := make([]string, len(sp.StratifyBy))
	for i, c := range sp.StratifyBy {
		groups[i] = parser.QuoteIdent(c)
	}
	g := strings.Join(groups, ", ")
	share := strconv.FormatFloat(sp.Rate, 'f', -1, 64)
	if sp.Rows > 0 {
		share = "(" + rows + "::DOUBLE / sluice_t)"
	}
	out := "SELECT * EXCLUDE (sluice_h, sluice_rn, sluice_n, sluice_t) FROM (" +
		"SELECT *, row_number() OVER (PARTITION BY " + g + " ORDER BY sluice_h) AS sluice_rn, " +
		"count(*) OVER (PARTITION BY " + g + ") AS sluice_n, " +
		"count(*) OVER () AS sluice_t " +
		"FROM (SELECT sluice_x.*, hash(sluice_x, " + seed + ") AS sluice_h FROM (" + sql + ") AS sluice_x) AS sluice_y" +
		") AS sluice_ranked " +
		"WHERE sluice_n >= " + strconv.FormatInt(sp.MinGroupRows, 10) +
		" AND sluice_rn <= ceil(sluice_n * " + share + ") " +
		"ORDER BY sluice_rn, sluice_h"
	if sp.Rows > 0 {
		out += " LIMIT " + rows
	}
	return out
}
