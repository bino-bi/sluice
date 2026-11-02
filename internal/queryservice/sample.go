// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"unicode/utf8"

	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// defaultSampleMaxRows is the sample row cap when Limits.SampleMaxRows is
// not set.
const defaultSampleMaxRows = 10_000

// maxSampleIdentBytes is the longest name the sample endpoint accepts.
// pg_query truncates longer names, DuckDB does not.
const maxSampleIdentBytes = 63

// SampleRequest is the decoded sample request passed by transports.
type SampleRequest struct {
	Table      string
	Columns    []string
	Rows       int64
	Rate       float64
	Method     apitypes.SampleMethod
	StratifyBy []string
	Seed       *int64
	Where      string
	Format     executor.OutputFormat
	User       *identity.UserCtx
	Facts      *policy.RequestFacts
	Origin     Origin
}

// Sample validates req, builds the inner SELECT and runs it through the
// query pipeline with the sample spec attached.
func (s *Service) Sample(ctx context.Context, req SampleRequest) (*QueryResult, error) {
	sql, spec, maxRows, err := s.buildSample(req)
	if err != nil {
		return nil, err
	}
	// The shape check runs here too, before the approval gate in execute,
	// so a where that changes the statement opens no approval ticket.
	ast, err := s.opts.Parser.Parse(ctx, sql)
	if err != nil {
		return nil, parseErrToAPI(err, "")
	}
	res, ok := ast.Raw().(*pg.ParseResult)
	if !ok {
		return nil, pkgerr.New(pkgerr.CodeUnsupportedSyntax).WithMessage("the sample endpoint needs the pg_query parser")
	}
	if err := rewriter.CheckSampleStatement(res); err != nil {
		return nil, err
	}
	sampleRequestsMetric().WithLabelValues(string(spec.Method)).Inc()
	return s.execute(ctx, QueryRequest{
		SQL:     sql,
		MaxRows: maxRows,
		Format:  req.Format,
		User:    req.User,
		Origin:  req.Origin,
		Facts:   req.Facts,
		Sample:  spec,
	}, false)
}

// buildSample validates req and returns the inner SELECT, the sample spec
// and the MaxRows of the query request.
func (s *Service) buildSample(req SampleRequest) (string, *rewriter.SampleSpec, int64, error) {
	bad := func(format string, args ...any) (string, *rewriter.SampleSpec, int64, error) {
		return "", nil, 0, pkgerr.New(pkgerr.CodeSyntax).WithMessage(fmt.Sprintf(format, args...))
	}

	if req.Table == "" {
		return bad("table is required")
	}
	parts := strings.Split(req.Table, ".")
	if len(parts) != 3 {
		return bad("table must be catalog.schema.table")
	}
	for _, p := range parts {
		if err := validSampleIdent(p); err != nil {
			return bad("table: %v", err)
		}
	}
	if err := checkSampleNames("columns", req.Columns); err != nil {
		return bad("%v", err)
	}

	rowsSet, rateSet := req.Rows != 0, req.Rate != 0
	switch {
	case rowsSet && rateSet:
		return bad("set rows or rate, not both")
	case !rowsSet && !rateSet:
		return bad("one of rows or rate is required")
	case rowsSet && req.Rows < 1:
		return bad("rows must be at least 1")
	case rateSet && (math.IsNaN(req.Rate) || req.Rate < rewriter.MinSampleRate || req.Rate > 1):
		return bad("rate must be in [1e-6, 1]")
	}

	method := req.Method
	switch method {
	case "":
		method = apitypes.SampleMethodReservoir
		if rateSet {
			method = apitypes.SampleMethodBernoulli
		}
	case apitypes.SampleMethodReservoir:
		if !rowsSet {
			return bad("method reservoir needs rows")
		}
	case apitypes.SampleMethodBernoulli, apitypes.SampleMethodSystem:
		if !rateSet {
			return bad("method %s needs rate", method)
		}
	case apitypes.SampleMethodStratified:
	default:
		return bad("method must be reservoir, bernoulli, system or stratified")
	}

	if method == apitypes.SampleMethodStratified {
		if len(req.StratifyBy) == 0 {
			return bad("stratify_by is required with method stratified")
		}
		if err := checkSampleNames("stratify_by", req.StratifyBy); err != nil {
			return bad("%v", err)
		}
		if len(req.Columns) > 0 {
			for _, g := range req.StratifyBy {
				if !containsFold(req.Columns, g) {
					return bad("stratify_by: %q is not in columns", g)
				}
			}
		}
	} else if len(req.StratifyBy) > 0 {
		return bad("stratify_by needs method stratified")
	}

	var seed int64
	if req.Seed != nil {
		seed = *req.Seed
		if seed < 0 || seed > rewriter.MaxSampleSeed {
			return bad("seed must be in [0, 2147483647]")
		}
	} else {
		var err error
		if seed, err = randomSeed(); err != nil {
			return "", nil, 0, pkgerr.Wrap(pkgerr.CodeInternal, err)
		}
	}

	spec := &rewriter.SampleSpec{
		Method:       method,
		Seed:         seed,
		StratifyBy:   req.StratifyBy,
		MinGroupRows: s.opts.Limits.SampleMinGroupRows,
	}
	var maxRows int64
	if rowsSet {
		// One below the ceiling, so that rows + 1 survives its clamp.
		limit := min(s.opts.Limits.SampleMaxRows, s.opts.Limits.DefaultMaxRows, s.opts.Limits.MaxRowsCeiling-1)
		spec.Rows = max(min(req.Rows, limit), 1)
		// The SQL bounds the output to Rows. The executor flags Truncated
		// once the count reaches MaxRows, so a full sample needs one more.
		maxRows = spec.Rows + 1
	} else {
		spec.Rate = req.Rate
	}

	cols := "*"
	if len(req.Columns) > 0 {
		quoted := make([]string, len(req.Columns))
		for i, c := range req.Columns {
			quoted[i] = parser.QuoteIdent(c)
		}
		cols = strings.Join(quoted, ", ")
	}
	sql := "SELECT " + cols + " FROM " + parser.QuoteIdent(parts[0]) + "." +
		parser.QuoteIdent(parts[1]) + "." + parser.QuoteIdent(parts[2])
	if where := strings.TrimSpace(req.Where); where != "" {
		sql += " WHERE (" + where + ")"
	}
	return sql, spec, maxRows, nil
}

// checkSampleNames checks each name and refuses duplicates. A duplicate
// output name breaks the result shape check of post-query masks.
func checkSampleNames(field string, names []string) error {
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		if err := validSampleIdent(n); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
		f := parser.FoldASCII(n)
		if seen[f] {
			return fmt.Errorf("%s: duplicate name %q", field, n)
		}
		seen[f] = true
	}
	return nil
}

// validSampleIdent checks one name of the sample request. Names that
// start with sluice_ are reserved for the sample wrappers.
func validSampleIdent(name string) error {
	switch {
	case name == "":
		return errors.New("empty name")
	case len(name) > maxSampleIdentBytes:
		return fmt.Errorf("name longer than %d bytes", maxSampleIdentBytes)
	case !utf8.ValidString(name):
		return errors.New("name is not valid UTF-8")
	case strings.HasPrefix(parser.FoldASCII(name), "sluice_"):
		return fmt.Errorf("name %q is reserved, names must not start with sluice_", name)
	}
	for i := 0; i < len(name); i++ {
		if c := name[i]; c < 0x20 || c == 0x7f {
			return errors.New("name has a control character")
		}
	}
	return nil
}

func containsFold(list []string, name string) bool {
	f := parser.FoldASCII(name)
	for _, n := range list {
		if parser.FoldASCII(n) == f {
			return true
		}
	}
	return false
}

// randomSeed picks a seed in [0, MaxSampleSeed].
func randomSeed() (int64, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(rewriter.MaxSampleSeed+1))
	if err != nil {
		return 0, err
	}
	return n.Int64(), nil
}
