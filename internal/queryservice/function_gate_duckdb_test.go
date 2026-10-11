// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/datasource"
	_ "github.com/bino-bi/sluice/internal/datasource/drivers/duckdbfile"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/internal/schema"
	"github.com/bino-bi/sluice/internal/secrets"
	pkgapi "github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

const helloSluiceDir = "../../examples/hello-sluice"

// countingExecutor wraps the real executor so a test can prove a query
// never reached DuckDB.
type countingExecutor struct {
	inner *executor.Executor
	calls int
}

func (c *countingExecutor) Execute(ctx context.Context, req executor.Request) (*executor.Response, error) {
	c.calls++
	return c.inner.Execute(ctx, req)
}

// newHelloSluiceService wires the real parser, policy engine, rewriter and
// DuckDB executor with the hello-sluice policies. The example ships a
// SQLite source; the same seed is loaded into a DuckDB file here so the
// test needs no extension download.
func newHelloSluiceService(t *testing.T, extraPolicies ...string) (*queryservice.Service, *countingExecutor, *fakeAudit) {
	t.Helper()
	return newHelloSluiceServiceWithSchema(t, nil, extraPolicies...)
}

// newHelloSluiceServiceWithSchema is newHelloSluiceService with a fixed
// schema cache for the rewriter. Nil keeps the live one.
func newHelloSluiceServiceWithSchema(t *testing.T, cache schema.Cache, extraPolicies ...string) (*queryservice.Service, *countingExecutor, *fakeAudit) {
	t.Helper()
	return newSeededHelloSluiceService(t, cache, "", extraPolicies...)
}

// newSeededHelloSluiceService runs extraSeed after the example seed.
func newSeededHelloSluiceService(t *testing.T, cache schema.Cache, extraSeed string, extraPolicies ...string) (*queryservice.Service, *countingExecutor, *fakeAudit) {
	t.Helper()
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "shop.duckdb")
	seed, err := os.ReadFile(filepath.Join(helloSluiceDir, "seed.sql"))
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	if _, err := db.ExecContext(ctx, string(seed)+extraSeed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	reg, err := datasource.New(ctx, datasource.Options{
		Snapshot: &datasource.Snapshot{DataSources: []*pkgapi.DataSource{{
			TypeMeta: pkgapi.TypeMeta{APIVersion: "sluice.bino.bi/v1alpha1", Kind: pkgapi.KindDataSource},
			Metadata: pkgapi.ObjectMeta{Name: "shop"},
			Spec:     pkgapi.DataSourceSpec{Type: pkgapi.DSDuckDBFile, Path: dbPath, Readonly: true},
		}}},
		HealthInterval: -1,
		FailFast:       true,
	})
	if err != nil {
		t.Fatalf("datasource.New: %v", err)
	}
	t.Cleanup(func() { _ = reg.Close() })

	exec, err := executor.New(ctx, executor.Config{AttachHook: reg.AttachHook()})
	if err != nil {
		t.Fatalf("executor.New: %v", err)
	}
	t.Cleanup(func() { _ = exec.Close() })
	parseCheck, err := executor.NewParseCheck(ctx, 0, nil)
	if err != nil {
		t.Fatalf("executor.NewParseCheck: %v", err)
	}
	t.Cleanup(func() { _ = parseCheck.Close() })

	snap := &config.Snapshot{ByKind: map[pkgapi.Kind][]pkgapi.Object{}}
	for _, name := range []string{"allow-analytics.yaml", "filter-tenant.yaml", "mask-email.yaml"} {
		f, err := os.Open(filepath.Join(helloSluiceDir, "policies.d", name))
		if err != nil {
			t.Fatalf("open policy: %v", err)
		}
		objs, err := pkgapi.Decode(f)
		_ = f.Close()
		if err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		for _, o := range objs {
			snap.Policies = append(snap.Policies, o)
			snap.ByKind[o.GetKind()] = append(snap.ByKind[o.GetKind()], o)
		}
	}
	for _, doc := range extraPolicies {
		objs, err := pkgapi.Decode(strings.NewReader(doc))
		if err != nil {
			t.Fatalf("decode extra policy: %v", err)
		}
		for _, o := range objs {
			snap.Policies = append(snap.Policies, o)
			snap.ByKind[o.GetKind()] = append(snap.ByKind[o.GetKind()], o)
		}
	}
	eng := policy.New(policy.Options{})
	if err := eng.ApplySnapshot(ctx, snap); err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}

	if cache == nil {
		cache = schema.New(schema.Options{Loader: schema.NewLoader(reg, exec.DB())})
	}
	p := pgquery.New(parser.Options{})
	ex := &countingExecutor{inner: exec}
	a := &fakeAudit{}
	svc := queryservice.New(queryservice.Options{
		Parser: p,
		Policy: eng,
		Rewriter: rewriter.New(rewriter.Options{
			Parser:     p,
			Schema:     cache,
			Salts:      secrets.NewSaltStore(secrets.NewResolver(secrets.ResolverOptions{})),
			ParseCheck: parseCheck,
		}),
		Executor: ex,
		Audit:    a,
		Catalogs: reg.Catalogs,
	})
	return svc, ex, a
}

// helloUser is the subject the hello-sluice API key resolves to.
func helloUser() *identity.UserCtx {
	return &identity.UserCtx{
		Subject: "hello",
		Groups:  []string{"analytics"},
		Claims:  map[string]any{"tenantId": "acme"},
	}
}

func drainRows(t *testing.T, res *queryservice.QueryResult) [][]any {
	t.Helper()
	var out [][]any
	for res.Rows.Next() {
		row := make([]any, len(res.Columns))
		dest := make([]any, len(row))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := res.Rows.Scan(dest...); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, row)
	}
	if err := res.Rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if err := res.Rows.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return out
}

// TestExecute_FunctionGate_RejectsBypassQueries replays the reported policy
// bypasses against the hello-sluice policies. Each query rides an allow on
// shop.main.customers and reads through a function policy cannot see.
func TestExecute_FunctionGate_RejectsBypassQueries(t *testing.T) {
	svc, ex, a := newHelloSluiceService(t)

	const anchor = "(SELECT 1 FROM shop.main.customers LIMIT 1) x"
	cases := []struct{ name, sql string }{
		{"query_table on allowed table", "SELECT t.id, t.email, t.tenant_id FROM " + anchor + ", query_table('shop.main.customers') t"},
		{"query_table on unlisted table", "SELECT t.id, t.username FROM " + anchor + ", query_table('shop.main.admin_users') t"},
		{"duckdb_databases", "SELECT d.database_name, d.path FROM " + anchor + ", duckdb_databases() d"},
		{"duckdb_settings", "SELECT s.name, s.value FROM " + anchor + ", duckdb_settings() s"},
		{"read_csv", "SELECT t.column0 FROM " + anchor + ", read_csv('/etc/hostname') t"},
		{"read_parquet", "SELECT t.id FROM " + anchor + ", read_parquet('/tmp/none.parquet') t"},
		{"sqlite_scan", "SELECT t.id, t.username FROM " + anchor + ", sqlite_scan('/data/shop.db', 'admin_users') t"},
		{"glob", "SELECT t.file FROM " + anchor + ", glob('/tmp/*') t"},
		{"query", "SELECT t.username FROM " + anchor + ", query('SELECT username FROM shop.main.admin_users') t"},
		{"scalar getenv", "SELECT getenv('HOME') AS v FROM shop.main.customers LIMIT 1"},
		{"scalar current_setting", "SELECT current_setting('temp_directory') AS v FROM shop.main.customers LIMIT 1"},
		{"scalar current_setting unknown name", "SELECT current_setting('x') AS v FROM shop.main.customers LIMIT 1"},
		{"COLUMNS expression", "SELECT COLUMNS('email') FROM shop.main.customers"},
		{"TABLESAMPLE", "SELECT id, email, tenant_id FROM shop.main.customers TABLESAMPLE reservoir(1000)"},
		{"table function with column argument", "SELECT u.e FROM shop.main.customers c, unnest(ARRAY[c.email]) AS u(e)"},
		{"qualified function", "SELECT t.id FROM " + anchor + ", main.query_table('shop.main.customers') t"},
		{"file path as table name", "SELECT t.id FROM " + anchor + `, "/tmp/customers.csv" t`},
		{"session keyword", "SELECT user AS u FROM shop.main.customers LIMIT 1"},
		{"method call on a masked column", "SELECT email.upper() AS e FROM shop.main.customers"},
		{"pg_catalog qualifier", "SELECT pg_catalog.upper(name) AS n FROM shop.main.customers"},
		{"aggregate called by name", "SELECT list_aggregate(ARRAY[1, 2], 'sum') AS s FROM shop.main.customers LIMIT 1"},
		{"type metadata", "SELECT typeof(name) AS t FROM shop.main.customers LIMIT 1"},
		{"SQL/JSON keyword call", "SELECT JSON_SERIALIZE('{}') AS j FROM shop.main.customers LIMIT 1"},
		{"EXPLAIN body", "EXPLAIN SELECT t.id FROM " + anchor + ", query_table('shop.main.customers') t"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			execBefore, auditBefore := ex.calls, len(a.all())

			res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
				SQL: tc.sql, User: helloUser(), Origin: queryservice.OriginREST,
			})
			if err == nil {
				rows := drainRows(t, res)
				t.Fatalf("query was served (%d rows: %v), want ACL_REJECTED", len(rows), rows)
			}
			if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeACLRejected {
				t.Fatalf("code = %s (%s), want %s", ae.Code, ae.Message, pkgerr.CodeACLRejected)
			}
			if ex.calls != execBefore {
				t.Fatalf("executor ran %d times, want 0", ex.calls-execBefore)
			}
			recs := a.all()[auditBefore:]
			if len(recs) != 1 {
				t.Fatalf("got %d audit records, want 1", len(recs))
			}
			if recs[0].Decision != audit.DecisionReject {
				t.Fatalf("audit decision = %s, want reject", recs[0].Decision)
			}
			if recs[0].Message == "" {
				t.Fatal("audit record must carry the reject reason")
			}
			if len(recs[0].PoliciesApplied) != 0 {
				t.Fatalf("audit lists applied policies %v for a query that never ran", recs[0].PoliciesApplied)
			}
		})
	}
}

// TestExecute_FunctionGate_AllowsOrdinaryQueries guards against the gate
// rejecting what it should not: plain selects, allowlisted functions and
// SQL syntax forms that pg_query models as function calls.
func TestExecute_FunctionGate_AllowsOrdinaryQueries(t *testing.T) {
	svc, _, a := newHelloSluiceService(t)

	run := func(t *testing.T, sql string) [][]any {
		t.Helper()
		res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
			SQL: sql, User: helloUser(), Origin: queryservice.OriginREST,
		})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		return drainRows(t, res)
	}

	t.Run("filter and mask still apply", func(t *testing.T) {
		before := len(a.all())
		rows := run(t, "SELECT id, email, tenant_id FROM shop.main.customers ORDER BY id")
		if len(rows) != 2 {
			t.Fatalf("got %d rows, want the 2 acme rows: %v", len(rows), rows)
		}
		for _, r := range rows {
			if r[1] != nil {
				t.Fatalf("email = %v, want NULL (masked)", r[1])
			}
			if r[2] != "acme" {
				t.Fatalf("tenant_id = %v, want acme", r[2])
			}
		}
		access := a.all()[before]
		if access.Decision != audit.DecisionAllow {
			t.Fatalf("audit decision = %s, want allow", access.Decision)
		}
		kinds := map[pkgapi.Kind]bool{}
		for _, p := range access.PoliciesApplied {
			kinds[p.Kind] = true
		}
		if !kinds[pkgapi.KindRowFilterPolicy] || !kinds[pkgapi.KindColumnMaskPolicy] {
			t.Fatalf("audit must list the filter and the mask, got %v", access.PoliciesApplied)
		}
	})

	t.Run("allowlisted table function with constants", func(t *testing.T) {
		rows := run(t, "SELECT c.id, g.i FROM shop.main.customers c, generate_series(1, 2) AS g(i) ORDER BY c.id, g.i")
		if len(rows) != 4 {
			t.Fatalf("got %d rows, want 4: %v", len(rows), rows)
		}
	})

	t.Run("scalar, aggregate and window functions", func(t *testing.T) {
		rows := run(t, "SELECT upper(name) AS n, length(name) AS l, count(*) OVER () AS total FROM shop.main.customers ORDER BY id")
		if len(rows) != 2 || rows[0][0] != "ALICE" {
			t.Fatalf("rows = %v", rows)
		}
		rows = run(t, "SELECT tenant_id, count(*) AS n, sum(total_cents) AS s FROM shop.main.orders GROUP BY tenant_id")
		if len(rows) != 1 {
			t.Fatalf("got %d groups, want 1 (acme only): %v", len(rows), rows)
		}
	})

	t.Run("SQL syntax forms", func(t *testing.T) {
		rows := run(t, "SELECT EXTRACT(year FROM CAST('2026-01-03' AS DATE)) AS y, SUBSTRING(name FROM 1 FOR 2) AS s, TRIM(name) AS t, "+
			"POSITION('l' IN name) AS p, COALESCE(name, 'x') AS c, GREATEST(1, 2) AS g, CURRENT_DATE AS d "+
			"FROM shop.main.customers WHERE name LIKE 'A%' ORDER BY id")
		if len(rows) != 1 || rows[0][1] != "Al" {
			t.Fatalf("rows = %v", rows)
		}
	})

	t.Run("functions DuckDB resolves outside its catalog", func(t *testing.T) {
		rows := run(t, "SELECT date(created_at) AS d, count(*) AS n FROM shop.main.customers GROUP BY 1 ORDER BY 1")
		if len(rows) != 2 {
			t.Fatalf("rows = %v", rows)
		}
		rows = run(t, "SELECT try(CAST(name AS INTEGER)) AS v, ifnull(name, 'x') AS n FROM shop.main.customers ORDER BY id")
		if len(rows) != 2 || rows[0][0] != nil {
			t.Fatalf("rows = %v", rows)
		}
	})
}

// pg_query reads a no-break space as part of an identifier, DuckDB reads it
// as a separator. Such a statement must not reach the engine, and the
// client must learn why.
func TestExecute_RejectsIdentifiersDuckDBWouldSplit(t *testing.T) {
	svc, ex, _ := newHelloSluiceService(t)

	sql := "SELECT id FROM shop.main.customers c" + string(rune(0x00a0)) + "x"
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{
		SQL: sql, User: helloUser(), Origin: queryservice.OriginREST,
	})
	ae := pkgerr.FromError(err)
	if err == nil || ae.Code != pkgerr.CodeSyntax {
		t.Fatalf("err = %v, want ERR_SYNTAX", err)
	}
	if ae.Message != parser.ErrIdentifierChars.Error() {
		t.Fatalf("message = %q, want the hint to quote the identifier", ae.Message)
	}
	if ex.calls != 0 {
		t.Fatalf("executor ran %d times, want 0", ex.calls)
	}
}
