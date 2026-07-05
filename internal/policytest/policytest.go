// SPDX-License-Identifier: AGPL-3.0-or-later

// Package policytest runs declarative policy test suites: each case names
// an identity + SQL and the expected outcome, filters, masks, and
// rewritten SQL. It drives the same parse -> policy.Evaluate ->
// rewriter.Rewrite pipeline the server uses, so a suite that passes here
// reflects real enforcement behaviour.
package policytest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/parserbackend"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/internal/secrets"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// Suite is a file of test cases.
type Suite struct {
	Cases []Case `json:"cases" yaml:"cases"`
}

// Identity is the subset of identity.UserCtx a case can specify.
type Identity struct {
	Subject string         `json:"subject" yaml:"subject"`
	Issuer  string         `json:"issuer,omitempty" yaml:"issuer,omitempty"`
	Email   string         `json:"email,omitempty" yaml:"email,omitempty"`
	Groups  []string       `json:"groups,omitempty" yaml:"groups,omitempty"`
	Claims  map[string]any `json:"claims,omitempty" yaml:"claims,omitempty"`
}

// Request carries optional per-request facts (headers etc.).
type Request struct {
	Headers   map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	UserAgent string            `json:"userAgent,omitempty" yaml:"userAgent,omitempty"`
}

// Case is a single scenario.
type Case struct {
	Name     string   `json:"name" yaml:"name"`
	Identity Identity `json:"identity" yaml:"identity"`
	Request  *Request `json:"request,omitempty" yaml:"request,omitempty"`
	SQL      string   `json:"sql" yaml:"sql"`
	Expect   Expect   `json:"expect" yaml:"expect"`
}

// Expect is the assertion set. Empty fields are not checked.
type Expect struct {
	Outcome              string   `json:"outcome,omitempty" yaml:"outcome,omitempty"` // allow|deny|reject
	DenyPolicy           string   `json:"denyPolicy,omitempty" yaml:"denyPolicy,omitempty"`
	ErrorCode            string   `json:"errorCode,omitempty" yaml:"errorCode,omitempty"`
	Rejections           []string `json:"rejections,omitempty" yaml:"rejections,omitempty"` // "policy/rule"
	Filters              []string `json:"filters,omitempty" yaml:"filters,omitempty"`       // table keys
	Masks                []string `json:"masks,omitempty" yaml:"masks,omitempty"`           // "table.column=type"
	PostMasks            []string `json:"postMasks,omitempty" yaml:"postMasks,omitempty"`   // "table.column=type"
	Applied              []string `json:"applied,omitempty" yaml:"applied,omitempty"`       // "Kind/Name"
	RewrittenSQLContains []string `json:"rewrittenSqlContains,omitempty" yaml:"rewrittenSqlContains,omitempty"`
	RewrittenSQL         string   `json:"rewrittenSql,omitempty" yaml:"rewrittenSql,omitempty"`
}

// Runner evaluates cases against a fixed policy snapshot.
type Runner struct {
	engine *policy.Engine
	rw     *rewriter.Rewriter
	parser parser.Parser
}

// NewRunner loads the policy directory, compiles it, and wires the
// parser + rewriter. It uses no schema cache, so SELECT-* combined with a
// column mask is out of scope for suites (documented) — write explicit
// column lists in test SQL.
func NewRunner(ctx context.Context, policyDir string, strict bool) (*Runner, error) {
	snap, err := config.LoadDirectory(ctx, config.LoadOptions{
		Strict:  strict,
		Sources: []config.SourceDir{{Path: policyDir}},
	})
	if err != nil {
		return nil, err
	}
	eng := policy.New(policy.Options{})
	if err := eng.ApplySnapshot(ctx, snap); err != nil {
		return nil, fmt.Errorf("compile snapshot: %w", err)
	}
	p := parserbackend.New(parser.Options{})
	rw := rewriter.New(rewriter.Options{
		Parser: p,
		Salts:  secrets.NewSaltStore(secrets.NewResolver(secrets.ResolverOptions{})),
	})
	return &Runner{engine: eng, rw: rw, parser: p}, nil
}

// Report is the aggregate result.
type Report struct {
	Total  int          `json:"total"`
	Passed int          `json:"passed"`
	Failed int          `json:"failed"`
	Cases  []CaseResult `json:"cases"`
}

// CaseResult is one case's outcome.
type CaseResult struct {
	Name     string   `json:"name"`
	Passed   bool     `json:"passed"`
	Failures []string `json:"failures,omitempty"`
}

// RunFile parses a YAML suite file and runs it.
func (r *Runner) RunFile(ctx context.Context, path string) (*Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Suite
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return r.Run(ctx, &s), nil
}

// RunDir runs every *.yaml/*.yml suite under dir and merges the reports.
func (r *Runner) RunDir(ctx context.Context, dir string) (*Report, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	agg := &Report{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		rep, err := r.RunFile(ctx, filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		agg.Total += rep.Total
		agg.Passed += rep.Passed
		agg.Failed += rep.Failed
		agg.Cases = append(agg.Cases, rep.Cases...)
	}
	return agg, nil
}

// Run evaluates every case in s.
func (r *Runner) Run(ctx context.Context, s *Suite) *Report {
	rep := &Report{Total: len(s.Cases)}
	for _, c := range s.Cases {
		cr := r.runCase(ctx, c)
		if cr.Passed {
			rep.Passed++
		} else {
			rep.Failed++
		}
		rep.Cases = append(rep.Cases, cr)
	}
	return rep
}

func (r *Runner) runCase(ctx context.Context, c Case) CaseResult {
	cr := CaseResult{Name: c.Name, Passed: true}
	fail := func(format string, args ...any) {
		cr.Passed = false
		cr.Failures = append(cr.Failures, fmt.Sprintf(format, args...))
	}

	user := &identity.UserCtx{
		Subject: c.Identity.Subject, Issuer: c.Identity.Issuer,
		Email: c.Identity.Email, Groups: c.Identity.Groups, Claims: c.Identity.Claims,
	}
	if user.Subject == "" {
		user = nil
	}
	var facts *policy.RequestFacts
	if c.Request != nil {
		facts = &policy.RequestFacts{Headers: c.Request.Headers, UserAgent: c.Request.UserAgent}
	}

	ast, parseErr := r.parser.Parse(ctx, c.SQL)
	in := policy.Input{User: user, Request: facts}
	if ast != nil {
		in.AST = ast
		in.Shape = ast.Shape()
		in.Tables = ast.Tables()
	}
	dec, err := r.engine.Evaluate(ctx, in)
	if err != nil {
		fail("evaluate error: %v", err)
		return cr
	}

	if c.Expect.Outcome != "" && string(dec.Outcome) != c.Expect.Outcome {
		fail("outcome: want %q got %q", c.Expect.Outcome, dec.Outcome)
	}
	if c.Expect.DenyPolicy != "" {
		got := ""
		if dec.DenyReason != nil {
			got = dec.DenyReason.PolicyName
		}
		if got != c.Expect.DenyPolicy {
			fail("denyPolicy: want %q got %q", c.Expect.DenyPolicy, got)
		}
	}
	checkSet(fail, "rejections", c.Expect.Rejections, rejectionKeys(dec))
	checkSet(fail, "filters", c.Expect.Filters, filterKeys(dec))
	checkSet(fail, "masks", c.Expect.Masks, maskKeys(dec))
	checkSet(fail, "applied", c.Expect.Applied, appliedKeys(dec))

	// Rewrite assertions run only for allow outcomes with a clean parse.
	needRewrite := len(c.Expect.PostMasks) > 0 || len(c.Expect.RewrittenSQLContains) > 0 || c.Expect.RewrittenSQL != ""
	if needRewrite || (dec.Outcome == policy.OutcomeAllow && c.Expect.ErrorCode == "") {
		if parseErr != nil {
			fail("rewrite requested but SQL did not parse: %v", parseErr)
			return cr
		}
		res, rerr := r.rw.Rewrite(ctx, rewriter.RewriteRequest{AST: ast, Decision: dec, User: user, Facts: facts, Raw: c.SQL})
		if rerr != nil {
			if c.Expect.ErrorCode != "" {
				if code := errorCode(rerr); code != c.Expect.ErrorCode {
					fail("errorCode: want %q got %q", c.Expect.ErrorCode, code)
				}
			} else if needRewrite {
				fail("rewrite error: %v", rerr)
			}
			return cr
		}
		if c.Expect.ErrorCode != "" {
			fail("errorCode: want %q but rewrite succeeded", c.Expect.ErrorCode)
		}
		checkSet(fail, "postMasks", c.Expect.PostMasks, postMaskKeys(res))
		for _, sub := range c.Expect.RewrittenSQLContains {
			if !strings.Contains(normalise(res.SQL), normalise(sub)) {
				fail("rewrittenSql does not contain %q; got %q", sub, res.SQL)
			}
		}
		if c.Expect.RewrittenSQL != "" && normalise(res.SQL) != normalise(c.Expect.RewrittenSQL) {
			fail("rewrittenSql: want %q got %q", c.Expect.RewrittenSQL, res.SQL)
		}
	}
	return cr
}

func checkSet(fail func(string, ...any), label string, want, got []string) {
	if len(want) == 0 {
		return
	}
	ws := append([]string(nil), want...)
	gs := append([]string(nil), got...)
	sort.Strings(ws)
	sort.Strings(gs)
	if strings.Join(ws, "|") != strings.Join(gs, "|") {
		fail("%s: want %v got %v", label, ws, gs)
	}
}

func rejectionKeys(d *policy.Decision) []string {
	var out []string
	for _, r := range d.Rejections {
		out = append(out, r.PolicyName+"/"+r.RuleName)
	}
	return out
}

func filterKeys(d *policy.Decision) []string {
	out := make([]string, 0, len(d.RowFilters))
	for k := range d.RowFilters {
		out = append(out, k)
	}
	return out
}

func maskKeys(d *policy.Decision) []string {
	out := make([]string, 0, len(d.ColumnMasks))
	for k, m := range d.ColumnMasks {
		out = append(out, k+"="+string(m.Type))
	}
	return out
}

func appliedKeys(d *policy.Decision) []string {
	var out []string
	for _, a := range d.Applied {
		out = append(out, string(a.Kind)+"/"+a.Name)
	}
	return out
}

func postMaskKeys(res *rewriter.RewriteResult) []string {
	var out []string
	for _, pm := range res.PostMasks {
		out = append(out, pm.TableKey+"."+pm.Column+"="+string(pm.Type))
	}
	return out
}

func errorCode(err error) string {
	if ae := pkgerr.FromError(err); ae != nil {
		return string(ae.Code)
	}
	return ""
}

func normalise(s string) string { return strings.Join(strings.Fields(s), " ") }
