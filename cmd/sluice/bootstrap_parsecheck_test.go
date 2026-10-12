// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/parserbackend"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// newParseCheckRuntime builds the server runtime with no policies and the
// default limits.
func newParseCheckRuntime(t *testing.T) *runtimeDeps {
	t.Helper()
	if !parserbackend.Implemented {
		t.Skip("buildRuntime refuses the stub parser backend (-tags=pure_parser)")
	}
	dir := t.TempDir()
	policyDir := filepath.Join(dir, "policies.d")
	if err := os.MkdirAll(policyDir, 0o750); err != nil {
		t.Fatalf("mkdir policies: %v", err)
	}
	cfgPath := filepath.Join(dir, "sluice.yaml")
	cfg := `
policies:
  dir: ` + policyDir + `
audit:
  file:
    path: ` + filepath.Join(dir, "audit") + `
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	deps, err := buildRuntime(context.Background(), cfgPath, policyDir)
	if err != nil {
		t.Fatalf("buildRuntime: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		deps.Close(shutdownCtx)
	})
	return deps
}

var limitDecision = &policy.Decision{Outcome: policy.OutcomeAllow, Rewrite: &policy.RewriteEffect{LimitMax: 5}}

// A nil ParseCheck skips the DuckDB parse check, so the served rewriter
// must have one. The deparse of (a OR b) IS NULL drops the parentheses.
func TestBuildRuntime_RewriterRunsParseCheck(t *testing.T) {
	deps := newParseCheckRuntime(t)
	ctx := context.Background()
	sql := "SELECT (a OR b) IS NULL FROM t"
	ast, err := deps.parser.Parse(ctx, sql)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res, err := deps.rewrite.Rewrite(ctx, rewriter.RewriteRequest{AST: ast, Decision: limitDecision, Raw: sql})
	if err == nil {
		t.Fatalf("served %q, want ERR_UNSUPPORTED_SYNTAX", res.SQL)
	}
	if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeUnsupportedSyntax {
		t.Errorf("code = %s (%v), want ERR_UNSUPPORTED_SYNTAX", ae.Code, err)
	}
}

// The parse instance is sized from limits.maxSqlBytes: a long IN list just
// under the default limit, as BI tools send, passes the check.
func TestBuildRuntime_ParseCheckFitsMaxSQLBytes(t *testing.T) {
	deps := newParseCheckRuntime(t)
	ctx := context.Background()
	var b strings.Builder
	b.WriteString("SELECT id FROM t WHERE id IN (1000000")
	for i := 1000001; b.Len() < parser.DefaultMaxSQLBytes-16; i++ {
		b.WriteString(", " + strconv.Itoa(i))
	}
	b.WriteString(")")
	sql := b.String()
	ast, err := deps.parser.Parse(ctx, sql)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := deps.rewrite.Rewrite(ctx, rewriter.RewriteRequest{AST: ast, Decision: limitDecision, Raw: sql}); err != nil {
		t.Fatalf("rewrite of %d bytes: %v", len(sql), err)
	}
}

// The served rewriter knows DuckDB's default database, so it refuses
// memory.t.id on a filtered t. The filter wrapper is named t alone.
func TestBuildRuntime_RefusesDefaultDatabaseQualifier(t *testing.T) {
	deps := newParseCheckRuntime(t)
	ctx := context.Background()
	pred, err := policy.CompilePredicateSpec(&apitypes.Predicate{Column: "tenant", Op: apitypes.PredOpEquals, Value: "k"})
	if err != nil {
		t.Fatalf("compile predicate: %v", err)
	}
	dec := &policy.Decision{Outcome: policy.OutcomeAllow, RowFilters: map[string]*policy.CompiledFilter{
		"..t": policy.NewFilter("..t", pred, apitypes.CombineRestrictive, "tenant"),
	}}
	sql := "SELECT memory.t.id FROM t"
	ast, err := deps.parser.Parse(ctx, sql)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res, err := deps.rewrite.Rewrite(ctx, rewriter.RewriteRequest{AST: ast, Decision: dec, Raw: sql})
	if err == nil {
		t.Fatalf("served %q, want ERR_UNSUPPORTED_SYNTAX", res.SQL)
	}
	if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeUnsupportedSyntax {
		t.Errorf("code = %s (%v), want ERR_UNSUPPORTED_SYNTAX", ae.Code, err)
	}
}
