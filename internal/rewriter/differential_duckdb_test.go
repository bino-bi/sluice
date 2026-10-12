// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/internal/schema"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgds "github.com/bino-bi/sluice/pkg/datasource"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
	pkgmask "github.com/bino-bi/sluice/pkg/mask"
)

// The differential test runs each corpus statement through the policy
// engine and the rewriter, then compares three runs:
//   - expected: the raw statement on the oracle database;
//   - mode A (dialect): the served statement on the oracle;
//   - mode B (policy): the served statement on the full database.
//
// The oracle holds only the rows the row filter lets through, and every
// masked column already holds what its mask returns. So mode A finds a
// statement the deparse made mean something else in DuckDB, and mode B
// finds a filter or mask the rewrite let a statement get around.
//
// Corpus lines are "<outcome> <sql>". The outcome is one of:
//   - same: served, and both modes return the expected rows;
//   - rawfails: DuckDB refuses the raw statement;
//   - an error code: the gateway refuses the statement with it.
//
// A served statement whose rows differ from the expected rows fails the
// test whatever its outcome says.

// Secrets as the masks return them. The post-query mask runs in Go after
// execution; the test stands in for it with diffPostMask.
const (
	diffConstMask  = "***"
	diffPostMask   = "#PM#"
	diffConstMaskN = 2
)

// diffSeedSQL creates the tables with the 6 rows of tenant k. Secrets are
// set per database by diffOracleSQL and diffFullSQL.
const diffSeedSQL = `
CREATE TABLE t (id INTEGER, a INTEGER, b INTEGER, s VARCHAR, d DATE, ts TIMESTAMP, tz TIMESTAMPTZ,
	iv INTERVAL, j JSON, l INTEGER[], st STRUCT(x INTEGER, y VARCHAR), flag BOOLEAN,
	n DECIMAL(10,2), f DOUBLE, bl BLOB, tenant VARCHAR, secret VARCHAR);
INSERT INTO t VALUES
	(1, 1, 2, 'Alice', DATE '2024-01-15', TIMESTAMP '2024-01-15 08:30:00', TIMESTAMPTZ '2024-01-15 08:30:00+00',
	 INTERVAL '400 days', '{"a":"1","b":2}', [1, 2, 3], {'x': 1, 'y': 'p'}, true, 1.50, 16777217.0, '\x00\x01'::BLOB, 'k', NULL),
	(2, 2, 1, 'a_b', DATE '2024-06-30', TIMESTAMP '2024-06-30 23:59:59', TIMESTAMPTZ '2024-06-30 23:59:59+02',
	 INTERVAL '1 day', '{"a":1}', [], {'x': 2, 'y': 'q'}, false, -2.25, 0.1, 'abc'::BLOB, 'k', NULL),
	(3, NULL, 3, 'A!_B', DATE '2025-02-28', TIMESTAMP '2025-02-28 12:00:00', TIMESTAMPTZ '2025-02-28 12:00:00-05',
	 INTERVAL '2 hours', '{"a":"x"}', [5], NULL, NULL, NULL, NULL, NULL, 'k', NULL),
	(4, 3, NULL, 'x%y', DATE '2025-12-31', NULL, NULL,
	 INTERVAL '3 months', NULL, NULL, {'x': NULL, 'y': 'r'}, true, 100.00, -1.5, ''::BLOB, 'k', NULL),
	(5, 1, 1, NULL, DATE '2026-03-01', TIMESTAMP '2026-03-01 00:00:00', TIMESTAMPTZ '2026-03-01 00:00:00+00',
	 INTERVAL '30 seconds', '[1,2]', [1, NULL], {'x': 5, 'y': NULL}, false, 0.00, 10000000000.0, 'xyz'::BLOB, 'k', NULL),
	(6, 0, 5, 'bob', NULL, TIMESTAMP '2023-07-04 06:00:00', TIMESTAMPTZ '2023-07-04 06:00:00+00',
	 NULL, '{"a":"1"}', [2, 2], {'x': 6, 'y': 's'}, NULL, 3.33, 2.5, NULL, 'k', NULL);
CREATE TABLE u AS SELECT id, b AS a, a AS b, upper(s) AS s, d + 7 AS d, ts, tz, iv, j, l, st, NOT flag AS flag,
	n * 2 AS n, f / 2 AS f, bl, tenant, secret FROM t;
CREATE TABLE v AS SELECT id, a, s, d, flag, tenant, 0 AS secret_n FROM t;
CREATE TABLE "semi" AS SELECT id, a, tenant FROM t;
CREATE TABLE "by" AS SELECT id, a, tenant FROM t;
CREATE TABLE kw AS SELECT id, tenant, secret AS "current_date" FROM t;
`

// diffOracleSQL gives every masked column the mask output.
var diffOracleSQL = fmt.Sprintf(`
UPDATE t SET secret = '%s';
UPDATE u SET secret = '%s';
UPDATE v SET secret_n = %d;
UPDATE kw SET "current_date" = '%[2]s';
`, diffConstMask, diffPostMask, diffConstMaskN)

// diffFullSQL sets the real secrets and adds 3 rows of tenant z to every
// table. The z rows copy k rows, so most predicates match them too.
const diffFullSQL = `
UPDATE t SET secret = CASE WHEN id = 3 THEN NULL ELSE 's' || id END;
UPDATE u SET secret = 'u' || id;
UPDATE v SET secret_n = CASE WHEN id = 4 THEN NULL ELSE 10 + id END;
UPDATE kw SET "current_date" = 'c' || id;
INSERT INTO t SELECT id + 6, a, b, s, d, ts, tz, iv, j, l, st, flag, n, f, bl, 'z', 'zs' || id FROM t WHERE id IN (1, 2, 5);
INSERT INTO u SELECT id + 6, a, b, s, d, ts, tz, iv, j, l, st, flag, n, f, bl, 'z', 'zu' || id FROM u WHERE id IN (1, 2, 5);
INSERT INTO v SELECT id + 6, a, s, d, flag, 'z', 20 + id FROM v WHERE id IN (1, 2, 5);
INSERT INTO "semi" SELECT id + 6, a, 'z' FROM "semi" WHERE id IN (1, 2, 5);
INSERT INTO "by" SELECT id + 6, a, 'z' FROM "by" WHERE id IN (1, 2, 5);
INSERT INTO kw SELECT id + 6, 'z', 'zc' || id FROM kw WHERE id IN (1, 2, 5);
`

// diffTables are the tables of the seed, all under the tenant filter.
var diffTables = []string{"t", "u", "v", "semi", "by", "kw"}

type diffWorld struct {
	oracle, full *executor.Executor
	parser       parser.Parser
	engine       *policy.Engine
	rw           *rewriter.Rewriter
	user         *identity.UserCtx
}

func newDiffWorld(t *testing.T) *diffWorld {
	t.Helper()
	oracle := newDiffDB(t, diffSeedSQL+diffOracleSQL)
	full := newDiffDB(t, diffSeedSQL+diffFullSQL)
	check, err := goldenParseCheck()
	if err != nil {
		t.Fatalf("parse check executor: %v", err)
	}

	p := pgquery.New(parser.Options{})
	return &diffWorld{
		oracle: oracle,
		full:   full,
		parser: p,
		engine: diffEngine(t),
		rw: rewriter.New(rewriter.Options{
			Parser:          p,
			Schema:          diffSchema(t, oracle),
			ParseCheck:      check,
			DefaultDatabase: "memory",
		}),
		user: &identity.UserCtx{Subject: "analyst"},
	}
}

// newDiffDB opens an in-memory database with one thread, so a statement
// returns its rows in the same order on every run.
func newDiffDB(t *testing.T, seed string) *executor.Executor {
	t.Helper()
	ctx := context.Background()
	e, err := executor.New(ctx, executor.Config{Harden: executor.HardenConfig{Threads: 1}})
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })
	if _, err := e.DB().ExecContext(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return e
}

// diffSchema reads the columns of each table with DESCRIBE. A table is
// listed under every name the corpus uses for it.
func diffSchema(t *testing.T, e *executor.Executor) schema.Cache {
	t.Helper()
	var entries []*schema.Entry
	for _, table := range diffTables {
		rows, err := e.DB().QueryContext(context.Background(), `DESCRIBE "`+table+`"`)
		if err != nil {
			t.Fatalf("describe %s: %v", table, err)
		}
		var cols []pkgds.Column
		for rows.Next() {
			var name, typ string
			var rest [4]any
			if err := rows.Scan(&name, &typ, &rest[0], &rest[1], &rest[2], &rest[3]); err != nil {
				t.Fatalf("describe %s: %v", table, err)
			}
			cols = append(cols, pkgds.Column{Name: name, SQLType: typ})
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("describe %s: %v", table, err)
		}
		for _, k := range []schema.Key{{Table: table}, {Schema: "main", Table: table}, {Catalog: "memory", Schema: "main", Table: table}} {
			entries = append(entries, &schema.Entry{Key: k, Columns: cols})
		}
	}
	return schema.NewStatic(entries)
}

func diffEngine(t *testing.T) *policy.Engine {
	t.Helper()
	filter := &apitypes.RowFilterPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindRowFilterPolicy},
		Metadata: apitypes.ObjectMeta{Name: "tenant-k"},
		Spec: apitypes.RowFilterSpec{
			Match: apitypes.Selector{Any: []apitypes.Clause{
				{Resources: &apitypes.ResourceSelector{Tables: diffTables}},
			}},
			Filter: apitypes.FilterSpec{Predicate: &apitypes.Predicate{
				Column: "tenant", Op: apitypes.PredOpEquals, Value: "k",
			}},
		},
	}
	objs := []apitypes.Object{
		allowAll(0),
		filter,
		maskPolicy("mask-t-secret", "t", []string{"secret"},
			apitypes.MaskSpec{Type: apitypes.MaskConstant, Args: apitypes.MaskArgs{Value: diffConstMask}}),
		fpeMask("u", "secret"),
		fpeMask("kw", "current_date"),
		maskPolicy("mask-v-secret-n", "v", []string{"secret_n"},
			apitypes.MaskSpec{Type: apitypes.MaskConstant, Args: apitypes.MaskArgs{Value: diffConstMaskN}}),
	}
	eng := policy.New(policy.Options{})
	snap := &config.Snapshot{Policies: objs, ByKind: map[apitypes.Kind][]apitypes.Object{}}
	for _, o := range objs {
		snap.ByKind[o.GetKind()] = append(snap.ByKind[o.GetKind()], o)
	}
	if err := eng.ApplySnapshot(context.Background(), snap); err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}
	return eng
}

type corpusLine struct {
	no      int
	outcome string
	sql     string
}

func readCorpus(t *testing.T, path string) []corpusLine {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open corpus: %v", err)
	}
	defer func() { _ = f.Close() }()
	var out []corpusLine
	sc := bufio.NewScanner(f)
	for no := 1; sc.Scan(); no++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		outcome, sql, ok := strings.Cut(line, " ")
		if !ok || !validOutcome(outcome) {
			t.Fatalf("%s:%d: want \"<outcome> <sql>\", got %q", path, no, line)
		}
		out = append(out, corpusLine{no: no, outcome: outcome, sql: strings.TrimSpace(sql)})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	return out
}

func validOutcome(s string) bool {
	switch s {
	case "same", "rawfails":
		return true
	}
	return strings.HasPrefix(s, "ERR_") || strings.HasPrefix(s, "ACL_")
}

func TestDifferential(t *testing.T) {
	w := newDiffWorld(t)
	// Mode B can only see a bypass where the full database differs from the
	// oracle: in the rows of tenant z and in every masked column.
	for _, sql := range []string{
		"SELECT id FROM t", "SELECT id FROM u", "SELECT id FROM v",
		`SELECT id FROM "semi"`, `SELECT id FROM "by"`,
		"SELECT secret FROM t WHERE tenant = 'k'", "SELECT secret FROM u WHERE tenant = 'k'",
		"SELECT secret_n FROM v WHERE tenant = 'k'", `SELECT "current_date" FROM kw WHERE tenant = 'k'`,
	} {
		want, err := runDiffSQL(context.Background(), w.oracle, sql, nil, nil)
		if err != nil {
			t.Fatalf("oracle: %v", err)
		}
		got, err := runDiffSQL(context.Background(), w.full, sql, nil, nil)
		if err != nil {
			t.Fatalf("full: %v", err)
		}
		if want.diff(got, false) == "" {
			t.Fatalf("the full database hides a bypass of %q", sql)
		}
	}
	files, err := filepath.Glob("testdata/differential/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no corpus files: %v", err)
	}
	sort.Strings(files)
	for _, file := range files {
		lines := readCorpus(t, file)
		t.Run(strings.TrimSuffix(filepath.Base(file), ".sql"), func(t *testing.T) {
			counts := map[string]int{}
			for _, l := range lines {
				counts[l.outcome]++
				t.Run(fmt.Sprintf("line%d", l.no), func(t *testing.T) {
					w.checkLine(t, l)
				})
			}
			t.Logf("%d lines: %v", len(lines), counts)
		})
	}
}

// diffResult is what the gateway does with one statement.
type diffResult struct {
	code     string // the refusal code; "" when served
	reason   error
	served   string
	rawErr   error
	mismatch string // set when a served statement returns other rows
}

func (r diffResult) String() string {
	switch {
	case r.mismatch != "":
		return "served with other rows: " + r.mismatch
	case r.code != "":
		return fmt.Sprintf("refused with %s: %v", r.code, r.reason)
	case r.rawErr != nil:
		return "served while raw fails: " + r.rawErr.Error()
	}
	return "served with the expected rows"
}

func (w *diffWorld) checkLine(t *testing.T, l corpusLine) {
	t.Helper()
	if fn := volatileFunction(l.sql); fn != "" {
		t.Fatalf("corpus line uses the volatile function %s: %s", fn, l.sql)
	}
	res := w.run(t, l.sql)
	if res.mismatch != "" {
		t.Fatalf("served statement returns other rows than raw\n raw:    %s\n served: %s\n %s", l.sql, res.served, res.mismatch)
	}
	got := "same"
	switch {
	case res.code != "":
		got = res.code
	case res.rawErr != nil:
		got = "rawfails"
	}
	if got != l.outcome {
		t.Fatalf("outcome = %s (%s), want %s\n sql:    %s\n served: %s", got, res, l.outcome, l.sql, res.served)
	}
}

// run takes sql through the gateway and the three runs. Corpus mistakes
// fail the test at once.
func (w *diffWorld) run(t *testing.T, sql string) diffResult {
	t.Helper()
	ctx := context.Background()
	ast, err := w.parser.Parse(ctx, sql)
	if err != nil {
		return diffResult{code: diffParseCode(err), reason: err}
	}

	// Scope guard: without policies the statement passes through as is.
	if res, err := w.rw.Rewrite(ctx, rewriter.RewriteRequest{
		AST: ast, Decision: &policy.Decision{Outcome: policy.OutcomeAllow}, Raw: sql,
	}); err == nil && (res.Changed || res.SQL != sql) {
		t.Fatalf("a bare allow changed the statement: %s", res.SQL)
	}

	dec, err := w.engine.Evaluate(ctx, policy.Input{
		User: w.user, AST: ast, Shape: ast.Shape(), Tables: ast.Tables(),
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	res, err := w.rw.Rewrite(ctx, rewriter.RewriteRequest{AST: ast, Decision: dec, User: w.user, Raw: sql})
	if err != nil {
		return diffResult{code: diffRewriteCode(err), reason: err}
	}
	checkNotes(t, sql, ast, res)
	top := topSelect(ast)
	if top != nil && (top.LimitCount != nil || top.LimitOffset != nil) && !sortEndsInID(top) {
		t.Fatalf("LIMIT or OFFSET needs an ORDER BY that ends in id: %s", sql)
	}
	ordered := top != nil && len(top.SortClause) > 0

	out := diffResult{served: res.SQL}
	want, rawErr := runDiffSQL(ctx, w.oracle, sql, nil, nil)
	modeA, errA := runDiffSQL(ctx, w.oracle, res.SQL, res.Params, res)
	modeB, errB := runDiffSQL(ctx, w.full, res.SQL, res.Params, res)
	if code := shapeRefusal(errA, errB); code != "" {
		return diffResult{code: code, reason: errDiffShape, served: res.SQL}
	}
	if rawErr != nil {
		out.rawErr = rawErr
		// Nothing to compare with, but the policy must still hold.
		if errA == nil && errB == nil {
			if d := modeA.diff(modeB, ordered); d != "" {
				out.mismatch = "mode A and mode B differ: " + d
			}
		}
		return out
	}
	for _, m := range []struct {
		name string
		got  *diffRows
		err  error
	}{{"mode A (dialect)", modeA, errA}, {"mode B (policy)", modeB, errB}} {
		if m.err != nil {
			out.mismatch = fmt.Sprintf("%s: %v", m.name, m.err)
			return out
		}
		if d := want.diff(m.got, ordered); d != "" {
			out.mismatch = m.name + ": " + d
			return out
		}
	}
	return out
}

// diffSQLMasks names the SQL-masked column of each table that has one.
var diffSQLMasks = map[string]string{"t": "secret", "v": "secret_n"}

// checkNotes asserts the rewrite applied the policies: a wrapper for each
// table with the note of its SQL mask, and a post-query mask when a
// statement that reads no SQL-masked table names a secret.
func checkNotes(t *testing.T, sql string, ast parser.AST, res *rewriter.RewriteResult) {
	t.Helper()
	if !res.Changed {
		t.Fatalf("served unchanged: %s", sql)
	}
	sqlMasked := false
	for _, tr := range ast.Tables() {
		key := strings.ToLower(tr.Catalog + "." + tr.Schema + "." + tr.Table)
		if !slices.Contains(res.Rewrites, "row-filter-wrapped:"+key) {
			t.Fatalf("no row filter on %s (notes %v): %s", key, res.Rewrites, sql)
		}
		if col, ok := diffSQLMasks[strings.ToLower(tr.Table)]; ok {
			sqlMasked = true
			if !slices.Contains(res.Rewrites, "column-mask:"+key+"."+col) {
				t.Fatalf("no mask on %s.%s (notes %v): %s", key, col, res.Rewrites, sql)
			}
		}
	}
	// The wrapper of u adds no note. Its secret is only served as a bound
	// select item.
	if !sqlMasked && strings.Contains(strings.ToLower(sql), "secret") && !slices.ContainsFunc(res.Rewrites, func(n string) bool {
		return strings.HasPrefix(n, "post-mask:")
	}) {
		t.Fatalf("no post-query mask applied (notes %v): %s", res.Rewrites, sql)
	}
}

// shapeRefusal returns the code queryservice refuses a post-masked result
// with when its columns are not the ones the masks are bound to.
func shapeRefusal(errs ...error) string {
	for _, err := range errs {
		if errors.Is(err, errDiffShape) {
			return string(pkgerr.CodeMaskContext)
		}
	}
	return ""
}

var errDiffShape = errors.New("result shape does not match the post-query masks")

type diffRows struct {
	names []string
	types []string
	rows  []string
}

// runDiffSQL runs sql and renders each cell as %T:%v. A non-nil res marks
// a served statement: its post-masked cells get the mask output.
func runDiffSQL(ctx context.Context, e *executor.Executor, sql string, params []any, res *rewriter.RewriteResult) (*diffRows, error) {
	resp, err := e.Execute(ctx, executor.Request{SQL: sql, Params: params})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Rows.Close() }()
	out := &diffRows{}
	for _, c := range resp.Columns {
		out.names = append(out.names, c.Name)
		out.types = append(out.types, c.SQLType)
	}
	post := map[int]bool{}
	if res != nil && len(res.PostMasks) > 0 {
		if len(resp.Columns) != res.ResultColumnCount {
			return nil, errDiffShape
		}
		for _, pm := range res.PostMasks {
			if parser.FoldASCII(resp.Columns[pm.ColumnIndex].Name) != parser.FoldASCII(pm.OutputName) {
				return nil, errDiffShape
			}
			post[pm.ColumnIndex] = true
		}
	}
	vals := make([]any, len(resp.Columns))
	ptrs := make([]any, len(vals))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for resp.Rows.Next() {
		if err := resp.Rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		cells := make([]string, len(vals))
		for i, v := range vals {
			switch {
			case v == nil:
				cells[i] = "NULL"
			case post[i]:
				cells[i] = fmt.Sprintf("%T:%v", diffPostMask, diffPostMask)
			default:
				cells[i] = fmt.Sprintf("%T:%v", v, v)
			}
		}
		out.rows = append(out.rows, strings.Join(cells, " | "))
	}
	if err := resp.Rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// diff describes how got differs from r, or returns "". Rows are compared
// in order only when the statement sorts them.
func (r *diffRows) diff(got *diffRows, ordered bool) string {
	if !slices.EqualFunc(r.names, got.names, func(want, have string) bool { return want == have || exprName(want) }) {
		return fmt.Sprintf("column names %q, want %q", got.names, r.names)
	}
	if !slices.Equal(r.types, got.types) {
		return fmt.Sprintf("column types %v, want %v", got.types, r.types)
	}
	want, have := r.rows, got.rows
	if !ordered {
		want, have = slices.Sorted(slices.Values(want)), slices.Sorted(slices.Values(have))
	}
	if !slices.Equal(want, have) {
		return fmt.Sprintf("rows\n  got:  %q\n  want: %q", have, want)
	}
	return ""
}

// exprName reports whether DuckDB generated the column name for an
// unaliased expression. A mask or a normalizer changes such a name, as
// THREAT_MODEL.md notes. An alias or a column name must come back as written.
func exprName(name string) bool {
	return strings.ContainsAny(name, `()'" `)
}

func topSelect(ast parser.AST) *pg.SelectStmt {
	tree, ok := ast.Raw().(*pg.ParseResult)
	if !ok || len(tree.GetStmts()) != 1 {
		return nil
	}
	return tree.Stmts[0].GetStmt().GetSelectStmt()
}

// sortEndsInID reports whether the last sort key is a column named id, so
// LIMIT and OFFSET pick the same rows on every run.
func sortEndsInID(sel *pg.SelectStmt) bool {
	if len(sel.SortClause) == 0 {
		return false
	}
	fields := sel.SortClause[len(sel.SortClause)-1].GetSortBy().GetNode().GetColumnRef().GetFields()
	return len(fields) > 0 && parser.FoldASCII(fields[len(fields)-1].GetString_().GetSval()) == "id"
}

// volatileRE finds functions whose result changes between the runs.
var volatileRE = regexp.MustCompile(`(?i)\b(random|setseed|now|gen_random_uuid|uuid|uuidv4|uuidv7|nextval|currval|` +
	`get_current_time|get_current_timestamp|transaction_timestamp)\s*\(|\b(current_timestamp|current_time|localtimestamp|localtime)\b`)

func volatileFunction(sql string) string {
	return volatileRE.FindString(sql)
}

// diffParseCode maps a parse error as queryservice does.
func diffParseCode(err error) string {
	switch {
	case errors.Is(err, parser.ErrMultipleStatements):
		return string(pkgerr.CodeMultipleStatements)
	case errors.Is(err, parser.ErrInputTooLarge):
		return string(pkgerr.CodePayloadTooLarge)
	case errors.Is(err, parser.ErrUnsupported), errors.Is(err, parser.ErrTooDeep), errors.Is(err, parser.ErrIdentifierTooLong):
		return string(pkgerr.CodeUnsupportedSyntax)
	}
	return string(pkgerr.CodeSyntax)
}

// diffRewriteCode maps a rewrite error as queryservice does.
func diffRewriteCode(err error) string {
	return string(rewriter.ErrorCode(err))
}

// TestDifferentialKeywords puts each DuckDB keyword, quoted, where pg_query
// deparses a name bare, and where the mutators write one: the wrapper
// alias, star expansion, a wrapper column, the mask alias and the filter
// column. The served statement must return the raw rows, or the gateway
// must refuse it.
func TestDifferentialKeywords(t *testing.T) {
	if testing.Short() {
		t.Skip("runs every DuckDB keyword in 13 positions")
	}
	ctx := context.Background()
	db := newDiffDB(t, `CREATE TABLE t (id INTEGER, a INTEGER, tenant VARCHAR); INSERT INTO t VALUES (1, 1, 'k'), (2, 1, 'k');`)
	words := duckDBKeywords(t, db)
	columns := func(names ...string) []pkgds.Column {
		out := make([]pkgds.Column, len(names))
		for i, n := range names {
			out[i] = pkgds.Column{Name: n}
		}
		return out
	}
	entries := []*schema.Entry{{Key: schema.Key{Table: "t"}, Columns: columns("id", "a", "tenant")}}
	var seed strings.Builder
	for _, w := range words {
		// Every row passes the filters and holds the mask value 1, so the
		// policies change no rows.
		fmt.Fprintf(&seed, `CREATE TABLE main."%[1]s" (id INTEGER, "%[1]s" INTEGER, tenant VARCHAR, m INTEGER); INSERT INTO main."%[1]s" VALUES (1, 1, 'k', 1), (2, 1, 'k', 1);`+"\n", w)
		entries = append(entries, &schema.Entry{Key: schema.Key{Schema: "main", Table: w}, Columns: columns("id", w, "tenant", "m")})
	}
	if _, err := db.DB().ExecContext(ctx, seed.String()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	check, err := goldenParseCheck()
	if err != nil {
		t.Fatalf("parse check executor: %v", err)
	}
	p := pgquery.New(parser.Options{})
	rw := rewriter.New(rewriter.Options{Parser: p, Schema: schema.NewStatic(entries), ParseCheck: check})

	tenantK := compilePredicate(t, apitypes.Predicate{Column: "tenant", Op: apitypes.PredOpEquals, Value: "k"})
	refused := map[string]int{}
	for _, w := range words {
		q, key := `"`+w+`"`, ".main."+w
		onT := keywordDecision(map[string]*policy.CompiledPredicate{"..t": tenantK})
		onW := keywordDecision(map[string]*policy.CompiledPredicate{key: tenantK})
		onColumn := keywordDecision(map[string]*policy.CompiledPredicate{
			key: compilePredicate(t, apitypes.Predicate{Column: w, Op: apitypes.PredOpEquals, Value: 1}),
		})
		masked := keywordDecision(map[string]*policy.CompiledPredicate{key: tenantK},
			policy.NewMask(key, w, apitypes.MaskConstant, pkgmask.Args{Value: 1}, "kw-mask"))
		// The mask is on m, so the wrapper lists the keyword column as it is.
		maskedOther := keywordDecision(map[string]*policy.CompiledPredicate{key: tenantK},
			policy.NewMask(key, "m", apitypes.MaskConstant, pkgmask.Args{Value: 1}, "kw-mask"))
		// A star is only expanded when the decision has a post-query mask.
		// This one is on a table the statement does not read.
		expanded := keywordDecision(map[string]*policy.CompiledPredicate{key: tenantK},
			policy.NewMask("..t", "a", apitypes.MaskFPE, pkgmask.Args{}, "kw-post"))
		for _, c := range []struct {
			position string
			sql      string
			dec      *policy.Decision
		}{
			{"table alias", "SELECT " + q + ".id FROM t AS " + q, onT},
			{"CTE name", "WITH " + q + " AS (SELECT id FROM t) SELECT id FROM " + q, onT},
			{"CTE column list", "WITH c(" + q + ") AS (SELECT id FROM t) SELECT " + q + " FROM c", onT},
			{"VALUES column list", "SELECT x." + q + " FROM t, (VALUES (1)) AS x(" + q + ")", onT},
			{"column ref", "SELECT " + q + " FROM main." + q + " AS k", onW},
			{"WINDOW name", "SELECT sum(id) OVER " + q + " FROM t WINDOW " + q + " AS (ORDER BY id)", onT},
			{"named-arg label", "SELECT struct_pack(" + q + " := id) FROM t", onT},
			{"wrapper alias", "SELECT count(*), max(t.id) FROM main." + q + " JOIN t ON true", onW},
			{"star expansion", "SELECT * FROM main." + q + " AS k", expanded},
			{"star over the wrapper", "SELECT * FROM main." + q, onW},
			{"wrapper column", "SELECT k." + q + " FROM main." + q + " AS k", maskedOther},
			{"mask alias", "SELECT k." + q + " FROM main." + q + " AS k", masked},
			{"filter column", "SELECT id FROM main." + q + " AS k", onColumn},
		} {
			ast, err := p.Parse(ctx, c.sql)
			if err != nil {
				checkKeywordRefusal(t, c.position, w, diffParseCode(err), err)
				refused[c.position]++
				continue
			}
			res, err := rw.Rewrite(ctx, rewriter.RewriteRequest{AST: ast, Decision: c.dec, Raw: c.sql})
			if err != nil {
				checkKeywordRefusal(t, c.position, w, diffRewriteCode(err), err)
				refused[c.position]++
				continue
			}
			if c.position == "star expansion" && strings.HasPrefix(res.SQL, "SELECT *") {
				t.Errorf("%s %q: the star was not expanded: %s", c.position, w, res.SQL)
			}
			want, err := runDiffSQL(ctx, db, c.sql, nil, nil)
			if err != nil {
				t.Errorf("%s %q: raw fails: %v", c.position, w, err)
				continue
			}
			got, err := runDiffSQL(ctx, db, res.SQL, res.Params, res)
			switch {
			case err != nil:
				t.Errorf("%s %q: served statement fails\n raw:    %s\n served: %s\n %v", c.position, w, c.sql, res.SQL, err)
			default:
				if d := want.diff(got, false); d != "" {
					t.Errorf("%s %q: served statement returns other rows\n raw:    %s\n served: %s\n %s", c.position, w, c.sql, res.SQL, d)
				}
			}
		}
	}
	t.Logf("%d keywords; refused per position: %v", len(words), refused)
}

// checkKeywordRefusal fails the test unless a keyword is refused as a
// statement the gateway does not support. ERR_INTERNAL would mean the
// parse check or a mutator failed.
func checkKeywordRefusal(t *testing.T, position, word, code string, err error) {
	t.Helper()
	if code != string(pkgerr.CodeSyntax) && code != string(pkgerr.CodeUnsupportedSyntax) {
		t.Errorf("%s %q: refused with %s, want ERR_SYNTAX or ERR_UNSUPPORTED_SYNTAX: %v", position, word, code, err)
	}
}

func duckDBKeywords(t *testing.T, e *executor.Executor) []string {
	t.Helper()
	rows, err := e.DB().QueryContext(context.Background(), "SELECT keyword_name FROM duckdb_keywords() ORDER BY 1")
	if err != nil {
		t.Fatalf("duckdb_keywords: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var words []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			t.Fatalf("duckdb_keywords: %v", err)
		}
		words = append(words, w)
	}
	if err := rows.Err(); err != nil || len(words) == 0 {
		t.Fatalf("duckdb_keywords: %d words, %v", len(words), err)
	}
	return words
}

func compilePredicate(t *testing.T, p apitypes.Predicate) *policy.CompiledPredicate {
	t.Helper()
	c, err := policy.CompilePredicateSpec(&p)
	if err != nil {
		t.Fatalf("compile predicate: %v", err)
	}
	return c
}

func keywordDecision(filters map[string]*policy.CompiledPredicate, masks ...*policy.CompiledMask) *policy.Decision {
	d := &policy.Decision{
		Outcome:     policy.OutcomeAllow,
		RowFilters:  map[string]*policy.CompiledFilter{},
		ColumnMasks: map[string]*policy.CompiledMask{},
	}
	for key, p := range filters {
		d.RowFilters[key] = policy.NewFilter(key, p, apitypes.CombineRestrictive, "kw-filter")
	}
	for _, m := range masks {
		d.ColumnMasks[m.TableKey+"."+m.Column] = m
	}
	return d
}
