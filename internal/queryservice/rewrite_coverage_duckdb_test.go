// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/queryservice"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// The hello-sluice seed has 5 customers (2 acme) and 6 orders (3 acme).
// The caller is tenant acme, so every count below must be the acme count.

// TestExecute_RowFilterReachesSubqueries locks that a row filter is applied
// to a table wherever the statement reads it, not only in a FROM clause of
// the outer query.
func TestExecute_RowFilterReachesSubqueries(t *testing.T) {
	svc, _, _ := newHelloSluiceService(t)
	const customers = "(SELECT count(*) FROM shop.main.customers)"

	cases := []struct {
		name string
		sql  string
		want string // fmt.Sprint of the result rows
	}{
		{"select list", "SELECT " + customers + " AS n", "[[2]]"},
		{"select list without outer table, aggregate over rows",
			"SELECT (SELECT string_agg(DISTINCT tenant_id, ',') FROM shop.main.customers) AS t", "[[acme]]"},
		{"where exists", "SELECT count(*) FROM shop.main.orders WHERE EXISTS (SELECT 1 FROM shop.main.customers c WHERE c.tenant_id = 'widget')", "[[0]]"},
		{"where in", "SELECT count(*) FROM shop.main.orders WHERE 3 IN (SELECT id FROM shop.main.customers)", "[[0]]"},
		{"left side of in", "SELECT count(*) FROM shop.main.orders WHERE " + customers + " IN (SELECT 5)", "[[0]]"},
		{"having", "SELECT count(*) FROM shop.main.orders HAVING count(*) < " + customers, "[]"},
		{"join condition", "SELECT count(*) FROM shop.main.orders o JOIN shop.main.customers c ON c.id = o.customer_id AND " + customers + " > 2", "[[0]]"},
		{"limit", "SELECT id FROM shop.main.orders ORDER BY id LIMIT " + customers, "[[101] [102]]"},
		{"order by", "SELECT " + customers + " AS n FROM shop.main.orders ORDER BY " + customers + ", id LIMIT 1", "[[2]]"},
		{"group by", "SELECT " + customers + " AS n FROM shop.main.orders GROUP BY " + customers, "[[2]]"},
		{"values list", "SELECT v.n FROM (VALUES (" + customers + ")) v(n)", "[[2]]"},
		{"nested subqueries", "SELECT (SELECT (SELECT count(*) FROM shop.main.orders) + count(*) FROM shop.main.customers) AS n", "[[5]]"},
		{"case expression", "SELECT CASE WHEN " + customers + " > 2 THEN 'many' ELSE 'few' END AS c", "[[few]]"},
		{"function argument", "SELECT abs(" + customers + ") AS n", "[[2]]"},
		{"CTE inside a subquery", "SELECT (WITH t AS (SELECT count(*) AS n FROM shop.main.customers) SELECT n FROM t) AS n", "[[2]]"},
		{"window argument", "SELECT sum(" + customers + ") OVER (ORDER BY id) AS s FROM shop.main.orders ORDER BY id LIMIT 1", "[[2]]"},
		{"lateral subquery", "SELECT x.n FROM shop.main.orders o, LATERAL (SELECT count(*) AS n FROM shop.main.customers c) x LIMIT 1", "[[2]]"},
		{"quoted table name in upper case", `SELECT count(*) FROM shop.main."CUSTOMERS"`, "[[2]]"},
		{"quoted catalog, schema and table in mixed case", `SELECT count(*) FROM "SHOP"."Main"."Customers"`, "[[2]]"},
		{"set operation arm inside a subquery", "SELECT (SELECT count(*) FROM (SELECT id FROM shop.main.customers UNION ALL SELECT id FROM shop.main.orders) u) AS n", "[[5]]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
				SQL: tc.sql, User: helloUser(), Origin: queryservice.OriginREST,
			})
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if got := fmt.Sprint(drainRows(t, res)); got != tc.want {
				t.Fatalf("rows = %s, want %s (another tenant's rows were read)", got, tc.want)
			}
		})
	}
}

// TestExecute_MaskReachesEveryUseOfTheColumn locks that customers.email is
// masked however the statement gets at it.
func TestExecute_MaskReachesEveryUseOfTheColumn(t *testing.T) {
	svc, ex, _ := newHelloSluiceService(t)

	// The mask must be applied: no result cell may hold an address.
	masked := []struct{ name, sql string }{
		{"values list in a subquery", "SELECT c.id, (VALUES (c.email)) AS e FROM shop.main.customers c"},
		{"lateral values list", "SELECT v.e FROM shop.main.customers c, LATERAL (VALUES (c.email)) v(e)"},
		{"star in a FROM subquery", "SELECT q.email, q.id FROM (SELECT * FROM shop.main.customers) q"},
		{"star in a FROM subquery, unqualified", "SELECT email FROM (SELECT * FROM shop.main.customers) q"},
		{"star in a joined subquery", "SELECT q.email FROM shop.main.orders o JOIN (SELECT * FROM shop.main.customers) q ON q.id = o.customer_id"},
		{"star in a nested subquery", "SELECT z.email FROM (SELECT * FROM (SELECT * FROM shop.main.customers) y) z"},
		{"column alias list on a subquery with star", "SELECT q.e2 FROM (SELECT * FROM shop.main.customers) AS q(i, n, e2, t, cr)"},
		{"column alias list on a subquery", "SELECT q.e2 FROM (SELECT id, email FROM shop.main.customers) AS q(i, e2)"},
		{"star in a CTE", "WITH q AS (SELECT * FROM shop.main.customers) SELECT q.email FROM q"},
		{"star in an expression subquery", "SELECT count(*) FROM shop.main.orders o WHERE EXISTS (SELECT * FROM shop.main.customers c WHERE c.id = o.customer_id AND c.email LIKE '%@%')"},
		{"renamed in a subquery", "SELECT q.e FROM (SELECT upper(email) AS e FROM shop.main.customers) q"},
		{"whole row of a subquery over the table", "SELECT q FROM (SELECT id, email FROM shop.main.customers) q"},
		{"quoted column in upper case", `SELECT id, "EMAIL" FROM shop.main.customers`},
		{"quoted column in mixed case behind an alias", `SELECT c."Email" FROM shop.main.customers c`},
		{"quoted alias in upper case", `SELECT "C".email FROM shop.main.customers c`},
		{"alias declared in upper case", `SELECT c.email FROM shop.main.customers "C"`},
		{"quoted table name as qualifier", `SELECT "CUSTOMERS".email FROM shop.main.customers`},
		{"quoted column in a function", `SELECT lower("EMAIL") FROM shop.main.customers`},
	}
	for _, tc := range masked {
		t.Run("masked/"+tc.name, func(t *testing.T) {
			res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
				SQL: tc.sql, User: helloUser(), Origin: queryservice.OriginREST,
			})
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			rows := drainRows(t, res)
			if got := fmt.Sprint(rows); strings.Contains(got, "@") {
				t.Fatalf("unmasked email in result: %s", got)
			}
			// EXISTS over a NULL email matches nothing.
			if tc.name == "star in an expression subquery" && fmt.Sprint(rows) != "[[0]]" {
				t.Fatalf("rows = %v, want [[0]]", rows)
			}
		})
	}

	// Forms the mask pass cannot follow must be refused, not served.
	refused := []struct{ name, sql string }{
		{"whole row", "SELECT c FROM shop.main.customers c"},
		{"whole row, table name", "SELECT customers FROM shop.main.customers"},
		{"whole row as function argument", "SELECT to_json(c) AS j FROM shop.main.customers c"},
		{"whole row field access", "SELECT (c).email AS e FROM shop.main.customers c"},
		{"whole row subscript", "SELECT c['email'] AS e FROM shop.main.customers c"},
		{"whole row in a predicate", "SELECT id FROM shop.main.customers c WHERE CAST(c AS VARCHAR) LIKE '%alice@%'"},
		{"whole row inside a subquery", "SELECT x FROM (SELECT c AS x FROM shop.main.customers c) q"},
		{"whole row from an outer scope", "SELECT (SELECT to_json(c)) AS j FROM shop.main.customers c"},
		{"whole row, alias named like a column of an outer table", "SELECT (SELECT placed_at FROM shop.main.customers placed_at ORDER BY id LIMIT 1) AS x FROM shop.main.orders o LIMIT 1"},
		{"two FROM items under one alias", "SELECT c.email FROM shop.main.customers c JOIN shop.main.orders c ON total_cents > 0 AND name IS NOT NULL LIMIT 2"},
		{"table and CTE under one alias", "WITH t AS (SELECT 1 AS x) SELECT c.email FROM shop.main.customers c, t c LIMIT 2"},
		{"positional column reference", "SELECT #3 FROM shop.main.customers"},
		{"inner alias hides the outer masked table", "SELECT (SELECT c.email FROM shop.main.orders c LIMIT 1) AS e FROM shop.main.customers c"},
		{"CTE alias hides the outer masked table", "WITH k AS (SELECT 1 AS one) SELECT (SELECT c.email FROM k c LIMIT 1) AS e FROM shop.main.customers c"},
		{"star in a pattern match", "SELECT c.* LIKE '%mail' FROM shop.main.customers c"},
		{"star as a sort key", "SELECT id FROM shop.main.customers c ORDER BY c.*"},
		{"whole row, quoted alias in upper case", `SELECT "C" FROM shop.main.customers c`},
		{"column alias list on the table", "SELECT c.e2 FROM shop.main.customers AS c(i, n, e2, t, cr)"},
		{"join alias", "SELECT j.email FROM (shop.main.customers c JOIN shop.main.orders o ON o.customer_id = c.id) AS j"},
		{"join alias with column list", "SELECT j.c3 FROM (shop.main.customers c JOIN shop.main.orders o ON o.customer_id = c.id) AS j(c1, c2, c3)"},
	}
	for _, tc := range refused {
		t.Run("refused/"+tc.name, func(t *testing.T) {
			before := ex.calls
			res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
				SQL: tc.sql, User: helloUser(), Origin: queryservice.OriginREST,
			})
			if err == nil {
				t.Fatalf("query was served: %v", drainRows(t, res))
			}
			if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeMaskContext {
				t.Fatalf("code = %s (%s), want %s", ae.Code, ae.Message, pkgerr.CodeMaskContext)
			}
			if ex.calls != before {
				t.Fatal("executor was reached")
			}
		})
	}

	// Must keep working.
	served := []struct{ name, sql, want string }{
		{"column named like the table alias", "SELECT name FROM shop.main.customers name ORDER BY 1", "[[Alice] [Bob]]"},
		{"whole row of a table without a mask", "SELECT count(o) FROM shop.main.orders o", "[[3]]"},
		{"column alias list on a table without a mask", "SELECT o.a FROM shop.main.orders AS o(a) ORDER BY 1 LIMIT 1", "[[101]]"},
		{"join without alias", "SELECT c.email, o.id FROM shop.main.customers c JOIN shop.main.orders o ON o.customer_id = c.id ORDER BY o.id LIMIT 1", "[[<nil> 101]]"},
		{"star", "SELECT * FROM shop.main.customers ORDER BY id LIMIT 1", "[[1 Alice <nil> acme 2026-01-03T10:00:00Z]]"},
		{"star through a subquery", "SELECT q.id, q.email FROM (SELECT * FROM shop.main.customers) q ORDER BY 1 LIMIT 1", "[[1 <nil>]]"},
		{"star over a subquery with star", "SELECT * FROM (SELECT * FROM shop.main.customers) q ORDER BY 1 LIMIT 1", "[[1 Alice <nil> acme 2026-01-03T10:00:00Z]]"},
		{"qualified star over a subquery", "SELECT q.* FROM (SELECT id, email FROM shop.main.customers) q ORDER BY 1 LIMIT 1", "[[1 <nil>]]"},
		{"star over nested subqueries", "SELECT z.id, z.email FROM (SELECT * FROM (SELECT * FROM shop.main.customers) y) z ORDER BY 1 LIMIT 1", "[[1 <nil>]]"},
		{"star over a table and a function", "SELECT * FROM shop.main.orders o, generate_series(7, 7) AS g(i) ORDER BY 1 LIMIT 1", "[[101 1 12900 acme 2026-03-01T12:00:00Z 7]]"},
		{"output alias named like the table in ORDER BY", "SELECT tenant_id, count(*) AS customers FROM shop.main.customers GROUP BY tenant_id ORDER BY customers DESC", "[[acme 2]]"},
		{"output alias named like the table alias in ORDER BY", "SELECT c.id, sum(o.total_cents) AS c FROM shop.main.customers c JOIN shop.main.orders o ON o.customer_id = c.id GROUP BY c.id ORDER BY c DESC", "[[2 19900] [1 17400]]"},
		{"derived table named like its column", "SELECT total FROM (SELECT count(*) AS total FROM shop.main.customers) total", "[[2]]"},
		{"derived column named like a masked column", "SELECT q.email FROM (SELECT id, upper(name) AS email FROM shop.main.customers) q ORDER BY q.id", "[[ALICE] [BOB]]"},
		{"join alias over a derived table", "SELECT j.id FROM (shop.main.orders o JOIN (SELECT id AS cid FROM shop.main.customers) c ON c.cid = o.customer_id) AS j ORDER BY 1", "[[101] [102] [103]]"},
		{"nested star next to a CTE", "WITH recent AS (SELECT customer_id FROM shop.main.orders) SELECT count(*) FROM (SELECT * FROM recent r JOIN shop.main.customers c ON c.id = r.customer_id) q", "[[3]]"},
		{"star next to a CTE", "WITH recent AS (SELECT customer_id FROM shop.main.orders) SELECT * FROM recent r JOIN shop.main.customers c ON c.id = r.customer_id ORDER BY 1, 2 LIMIT 1", "[[1 1 Alice <nil> acme 2026-01-03T10:00:00Z]]"},
		{"star with a function without alias", "SELECT * FROM shop.main.orders, generate_series(7, 7) ORDER BY 1 LIMIT 1", "[[101 1 12900 acme 2026-03-01T12:00:00Z 7]]"},
		{"nested star with a subquery without alias", "SELECT count(*) FROM shop.main.orders o WHERE EXISTS (SELECT * FROM shop.main.customers c, (SELECT 1 AS one) WHERE c.id = o.customer_id)", "[[3]]"},
		{"star in EXISTS needs no expansion", "SELECT count(*) FROM shop.main.orders o WHERE EXISTS (SELECT * FROM shop.main.customers c WHERE c.id = o.customer_id)", "[[3]]"},
		{"same alias for the same table in a subquery", "SELECT (SELECT c.email FROM shop.main.customers c WHERE c.id = 1) AS e FROM shop.main.customers c ORDER BY 1 LIMIT 1", "[[<nil>]]"},
		{"quoted CTE name in mixed case", `WITH "Totals" AS (SELECT customer_id, sum(total_cents) AS total FROM shop.main.orders GROUP BY 1) SELECT customer_id, total FROM "Totals" ORDER BY 1`, "[[1 17400] [2 19900]]"},
		{"star over a join keeps FROM order", "SELECT * FROM shop.main.orders o JOIN shop.main.customers c ON c.id = o.customer_id ORDER BY 1 LIMIT 1",
			"[[101 1 12900 acme 2026-03-01T12:00:00Z 1 Alice <nil> acme 2026-01-03T10:00:00Z]]"},
	}
	for _, tc := range served {
		t.Run("served/"+tc.name, func(t *testing.T) {
			res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
				SQL: tc.sql, User: helloUser(), Origin: queryservice.OriginREST,
			})
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if got := fmt.Sprint(drainRows(t, res)); got != tc.want {
				t.Fatalf("rows = %s, want %s", got, tc.want)
			}
		})
	}
}

// EXPLAIN ANALYZE runs its body, so only a SELECT body is read-only. The
// rewrite passes also only know how to secure a SELECT body.
func TestExecute_ExplainBodyMustBeSelect(t *testing.T) {
	// The write targets must be allowed so the statement-kind gate, not the
	// per-table access gate, is what refuses each query.
	svc, ex, _ := newHelloSluiceService(t, explainTargetsAllow)
	cases := []struct {
		sql  string
		code pkgerr.Code
	}{
		{"EXPLAIN ANALYZE CREATE TABLE memory.main.x AS SELECT * FROM shop.main.customers", pkgerr.CodeUnsupportedSyntax},
		{"EXPLAIN CREATE TEMP TABLE z AS SELECT * FROM shop.main.customers", pkgerr.CodeUnsupportedSyntax},
		{"EXPLAIN ANALYZE INSERT INTO memory.main.x SELECT * FROM shop.main.customers", pkgerr.CodeACLRejected},
		{"EXPLAIN ANALYZE UPDATE memory.main.x SET name = c.email FROM shop.main.customers c WHERE x.id = c.id", pkgerr.CodeACLRejected},
		{"EXPLAIN ANALYZE DELETE FROM memory.main.x USING shop.main.customers c WHERE x.id = c.id", pkgerr.CodeACLRejected},
	}
	for _, tc := range cases {
		res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
			SQL: tc.sql, User: helloUser(), Origin: queryservice.OriginREST,
		})
		if err == nil {
			_ = res.Rows.Close()
			t.Errorf("%s: served, want %s", tc.sql, tc.code)
			continue
		}
		if ae := pkgerr.FromError(err); ae.Code != tc.code {
			t.Errorf("%s: code = %s, want %s", tc.sql, ae.Code, tc.code)
		}
	}
	if ex.calls != 0 {
		t.Fatalf("executor ran %d times, want 0", ex.calls)
	}
}

// The plan of a filtered or masked table shows the policy SQL with its
// bound values (salts, tenant ids) and statistics over the hidden rows.
func TestExecute_ExplainOnProtectedTableRefused(t *testing.T) {
	svc, ex, _ := newHelloSluiceService(t)
	for _, sql := range []string{
		"EXPLAIN SELECT id FROM shop.main.customers",
		"EXPLAIN ANALYZE SELECT count(*) FROM shop.main.orders",
	} {
		res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
			SQL: sql, User: helloUser(), Origin: queryservice.OriginREST,
		})
		if err == nil {
			t.Errorf("%s: served %v, want ERR_UNSUPPORTED_SYNTAX", sql, drainRows(t, res))
			continue
		}
		if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeUnsupportedSyntax {
			t.Errorf("%s: code = %s, want ERR_UNSUPPORTED_SYNTAX", sql, ae.Code)
		}
	}
	if ex.calls != 0 {
		t.Fatalf("executor ran %d times, want 0", ex.calls)
	}
}

const explainTargetsAllow = `
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: allow-explain-targets
  priority: 100
spec:
  effect: allow
  match:
    any:
      - subjects: { groups: ["analytics"] }
        resources: { catalogs: ["memory"], schemas: ["main"] }
`

const maskIDsPolicy = `
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata:
  name: mask-ids
  priority: 50
spec:
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["customers", "orders"]
          columns: ["id"]
  mask:
    type: "null"
`

// Two tables mask a column of the same name. A reference to it must be
// masked or refused, never served raw because the lookup could not decide.
func TestExecute_MaskWithSameColumnNameInTwoTables(t *testing.T) {
	svc, _, _ := newHelloSluiceService(t, maskIDsPolicy)
	cases := []struct{ name, sql string }{
		{"derived table over the other table", "SELECT id FROM shop.main.orders, (SELECT 1 AS one FROM shop.main.customers LIMIT 1) q ORDER BY placed_at"},
		{"subquery inside the scope of the other table", "SELECT (SELECT id FROM shop.main.orders ORDER BY placed_at LIMIT 1) AS x FROM shop.main.customers"},
		{"both tables joined, qualified", "SELECT o.id, c.id FROM shop.main.orders o JOIN shop.main.customers c ON c.tenant_id = o.tenant_id ORDER BY o.placed_at"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
				SQL: tc.sql, User: helloUser(), Origin: queryservice.OriginREST,
			})
			if err != nil {
				if ae := pkgerr.FromError(err); ae.Code == pkgerr.CodeMaskContext {
					return
				}
				t.Fatalf("execute: %v", err)
			}
			for _, row := range drainRows(t, res) {
				for _, cell := range row {
					if cell != nil {
						t.Fatalf("unmasked id %v in the result", cell)
					}
				}
			}
		})
	}
}

// SELECT * over a RIGHT or FULL join with USING merges the join column.
// Listing both sides instead would return NULL keys, so it is refused.
func TestExecute_StarOverOuterUsingJoinIsRefused(t *testing.T) {
	svc, _, _ := newHelloSluiceService(t)
	for _, sql := range []string{
		"SELECT q.id FROM (SELECT * FROM shop.main.customers c RIGHT JOIN shop.main.orders o USING (id)) q",
		"SELECT * FROM shop.main.orders o FULL JOIN shop.main.customers c USING (id)",
		"SELECT * FROM shop.main.customers c NATURAL RIGHT JOIN shop.main.orders o",
	} {
		res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
			SQL: sql, User: helloUser(), Origin: queryservice.OriginREST,
		})
		if err == nil {
			t.Errorf("%s: served %v, want ERR_UNSUPPORTED_SYNTAX", sql, drainRows(t, res))
			continue
		}
		if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeUnsupportedSyntax {
			t.Errorf("%s: code = %s, want ERR_UNSUPPORTED_SYNTAX", sql, ae.Code)
		}
	}
}

const adminUsersPolicies = `
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: allow-admin-users
  priority: 100
spec:
  effect: allow
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["admin_users"]
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata:
  name: mask-username
  priority: 50
spec:
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["admin_users"]
          columns: ["username"]
  mask:
    type: "null"
`

// A masked table without a row filter stays a plain table in the rewritten
// SQL, so its schema-qualified name also names the whole row.
func TestExecute_MaskOnTableWithoutRowFilter(t *testing.T) {
	svc, _, _ := newHelloSluiceService(t, adminUsersPolicies)

	run := func(sql string) ([][]any, error) {
		res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
			SQL: sql, User: helloUser(), Origin: queryservice.OriginREST,
		})
		if err != nil {
			return nil, err
		}
		return drainRows(t, res), nil
	}

	for _, sql := range []string{
		"SELECT admin_users FROM shop.main.admin_users",
		"SELECT main.admin_users FROM shop.main.admin_users",
		"SELECT shop.main.admin_users FROM shop.main.admin_users",
		`SELECT "MAIN".admin_users FROM shop.main.admin_users`,
		"SELECT to_json(main.admin_users) AS j FROM shop.main.admin_users",
		"SELECT (main.admin_users).username AS u FROM shop.main.admin_users",
		"SELECT q.x FROM (SELECT main.admin_users AS x FROM shop.main.admin_users) q",
		"SELECT id FROM shop.main.admin_users WHERE CAST(main.admin_users AS VARCHAR) LIKE '%root%'",
		"SELECT #2 FROM shop.main.admin_users",
	} {
		rows, err := run(sql)
		if err == nil {
			t.Errorf("%s: served %v, want ERR_MASK_UNSUPPORTED_CONTEXT", sql, rows)
			continue
		}
		if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeMaskContext {
			t.Errorf("%s: code = %s (%s), want ERR_MASK_UNSUPPORTED_CONTEXT", sql, ae.Code, ae.Message)
		}
	}

	for sql, want := range map[string]string{
		"SELECT id, username, role FROM shop.main.admin_users ORDER BY id":            "[[1 <nil> superuser] [2 <nil> maintainer]]",
		"SELECT main.admin_users.username FROM shop.main.admin_users ORDER BY id":     "[[<nil>] [<nil>]]",
		"SELECT shop.main.admin_users.username FROM shop.main.admin_users ORDER BY 1": "[[<nil>] [<nil>]]",
		"SELECT * FROM shop.main.admin_users ORDER BY id LIMIT 1":                     "[[1 <nil> superuser]]",
	} {
		rows, err := run(sql)
		if err != nil {
			t.Errorf("%s: %v", sql, err)
			continue
		}
		if got := fmt.Sprint(rows); got != want {
			t.Errorf("%s: rows = %s, want %s", sql, got, want)
		}
	}
}

const fakeNamePolicy = `
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata:
  name: fake-name
  priority: 50
spec:
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["customers"]
          columns: ["name"]
  mask:
    type: fake
    args:
      fakeType: name
`

// A post-query mask is applied in Go to a bare select item of the outer
// query. Every other use of the column must be refused, whatever node
// type wraps it.
func TestExecute_PostQueryMaskOnlyInBareSelectItems(t *testing.T) {
	svc, _, _ := newHelloSluiceService(t, fakeNamePolicy, adminUsersPolicies)

	run := func(sql string) ([][]any, error) {
		res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
			SQL: sql, User: helloUser(), Origin: queryservice.OriginREST,
		})
		if err != nil {
			return nil, err
		}
		return drainRows(t, res), nil
	}

	for _, sql := range []string{
		"SELECT upper(name) AS n FROM shop.main.customers",
		"SELECT ARRAY[name] AS n FROM shop.main.customers",
		`SELECT name COLLATE "nocase" AS n FROM shop.main.customers`,
		"SELECT (ARRAY[name])[1] AS n FROM shop.main.customers",
		"SELECT name[1:3] AS n FROM shop.main.customers",
		"SELECT id FROM shop.main.customers ORDER BY name",
		"SELECT DISTINCT ON (name) id FROM shop.main.customers",
		"SELECT id FROM shop.main.customers WHERE ARRAY[name] = ARRAY['Alice']",
		"SELECT q.name FROM (SELECT name FROM shop.main.customers) q",
		"WITH q AS (SELECT name FROM shop.main.customers) SELECT name FROM q",
		"SELECT (SELECT name FROM shop.main.customers LIMIT 1) AS n",
	} {
		rows, err := run(sql)
		if err == nil {
			t.Errorf("%s: served %v, want ERR_MASK_UNSUPPORTED_CONTEXT", sql, rows)
			continue
		}
		if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeMaskContext {
			t.Errorf("%s: code = %s (%s), want ERR_MASK_UNSUPPORTED_CONTEXT", sql, ae.Code, ae.Message)
		}
	}

	for _, sql := range []string{
		"SELECT id, name FROM shop.main.customers ORDER BY id",
		"SELECT * FROM shop.main.customers ORDER BY id",
		"SELECT * FROM shop.main.customers c JOIN shop.main.admin_users a ON a.id = c.id ORDER BY 1",
	} {
		rows, err := run(sql)
		if err != nil {
			t.Errorf("%s: %v", sql, err)
			continue
		}
		if got := fmt.Sprint(rows); len(rows) != 2 || strings.Contains(got, "Alice") || strings.Contains(got, "Bob") {
			t.Errorf("%s: rows = %s, want 2 rows with fake names", sql, got)
		}
	}
}

// A post-query mask is applied in Go by result column index. DuckDB also
// reads a select alias, a column position, a USING column and a nested
// join condition on the raw value, and unnest can shift the columns. Each
// of these must be refused before the query reaches DuckDB.
func TestExecute_PostQueryMaskLeakForms(t *testing.T) {
	svc, ex, _ := newHelloSluiceService(t, fakeNamePolicy, adminUsersPolicies)

	for _, sql := range []string{
		// Select alias of the masked item reused elsewhere.
		"SELECT c.name AS n, n AS leak FROM shop.main.customers c",
		"SELECT c.name AS n, upper(n) AS leak FROM shop.main.customers c",
		"SELECT c.name AS n, (SELECT n) AS leak FROM shop.main.customers c",
		"SELECT c.id, c.name AS n FROM shop.main.customers c WHERE n = 'Alice'",
		"SELECT c.id, c.name AS n FROM shop.main.customers c WHERE c.id IN (SELECT o.customer_id FROM shop.main.orders o WHERE n = 'Alice')",
		"SELECT c.id, c.name AS n FROM shop.main.customers c ORDER BY n LIMIT 1",
		"SELECT c.name AS n, count(*) AS k FROM shop.main.customers c GROUP BY n",
		// Column positions of the masked item.
		"SELECT c.id, c.name FROM shop.main.customers c ORDER BY 2 LIMIT 1",
		"SELECT c.name, count(*) AS k FROM shop.main.customers c GROUP BY 1",
		"SELECT c.name, count(*) AS k FROM shop.main.customers c GROUP BY ROLLUP (1)",
		"SELECT DISTINCT ON (2) c.id, c.name FROM shop.main.customers c",
		// DuckDB reads a position under COLLATE as a position.
		`SELECT c.id, c.name FROM shop.main.customers c ORDER BY 2 COLLATE "nocase" LIMIT 1`,
		`SELECT c.id, c.name FROM shop.main.customers c ORDER BY c.id, 2 COLLATE "C" DESC`,
		`SELECT DISTINCT ON (2 COLLATE "nocase") c.id, c.name FROM shop.main.customers c ORDER BY c.id`,
		// DISTINCT compares the raw values.
		"SELECT DISTINCT c.name FROM shop.main.customers c",
		// USING and NATURAL compare the raw values.
		"SELECT c.id FROM shop.main.customers c JOIN (SELECT 'Alice' AS name) x USING (name)",
		"SELECT c.id FROM shop.main.customers c NATURAL JOIN (SELECT 'Alice' AS name) x",
		// The condition of an inner join.
		"SELECT c.id FROM shop.main.customers c JOIN shop.main.orders o ON o.customer_id = c.id AND c.name = 'Alice' JOIN shop.main.orders p ON p.id = o.id",
		// unnest of a struct adds columns before the masked one.
		"SELECT unnest(struct_pack(a := 1, b := 2)), c.name FROM shop.main.customers c",
	} {
		before := ex.calls
		res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
			SQL: sql, User: helloUser(), Origin: queryservice.OriginREST,
		})
		if err == nil {
			t.Errorf("%s: served %v, want ERR_MASK_UNSUPPORTED_CONTEXT", sql, drainRows(t, res))
			continue
		}
		if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeMaskContext {
			t.Errorf("%s: code = %s (%s), want ERR_MASK_UNSUPPORTED_CONTEXT", sql, ae.Code, ae.Message)
		}
		if ex.calls != before {
			t.Errorf("%s: executor was reached", sql)
		}
	}

	for sql, want := range map[string]int{
		"SELECT c.id, c.name AS n FROM shop.main.customers c ORDER BY 1":                             2,
		"SELECT c.name, c.id FROM shop.main.customers c ORDER BY 2":                                  2,
		"SELECT * FROM shop.main.customers c JOIN shop.main.admin_users a ON a.id = c.id ORDER BY 1": 2,
		// A CTE body cannot see the select aliases.
		"WITH t AS (SELECT customer_id FROM shop.main.orders) SELECT c.name AS customer_id FROM shop.main.customers c JOIN t ON t.customer_id = c.id": 3,
		// The row-filter wrapper of orders reads tenant_id. That is policy
		// SQL, not a use of the select alias.
		"SELECT c.name AS tenant_id FROM shop.main.customers c WHERE c.id IN (SELECT o.customer_id FROM shop.main.orders o)":               2,
		"SELECT c.name AS tenant_id FROM shop.main.customers c WHERE EXISTS (SELECT 1 FROM shop.main.orders o WHERE o.customer_id = c.id)": 2,
		"SELECT c.name AS tenant_id, (SELECT count(*) FROM shop.main.orders o WHERE o.customer_id = c.id) AS k FROM shop.main.customers c": 2,
		// A subquery returns one column, so its unnest cannot shift the
		// bound index.
		"SELECT c.name, (SELECT max(u) FROM unnest(ARRAY[1, 2]) AS t(u)) AS m FROM shop.main.customers c": 2,
		"SELECT c.name, ARRAY(SELECT u FROM unnest(ARRAY[1, 2]) AS t(u)) AS m FROM shop.main.customers c": 2,
	} {
		res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
			SQL: sql, User: helloUser(), Origin: queryservice.OriginREST,
		})
		if err != nil {
			t.Errorf("%s: %v", sql, err)
			continue
		}
		rows := drainRows(t, res)
		if got := fmt.Sprint(rows); len(rows) != want || strings.Contains(got, "Alice") || strings.Contains(got, "Bob") {
			t.Errorf("%s: rows = %s, want %d rows with fake names", sql, got, want)
		}
	}
}

const sampleWithFakeTenantPolicies = `
apiVersion: sluice.bino.bi/v1alpha1
kind: QueryRewritePolicy
metadata:
  name: sample-all
  priority: 50
spec:
  match:
    any:
      - resources:
          tables: ["*"]
  rewrite:
    sample:
      rate: 1.0
      method: bernoulli
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata:
  name: fake-customer-tenant
  priority: 50
spec:
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["customers"]
          columns: ["tenant_id"]
  mask:
    type: fake
    args:
      fakeType: name
`

// The sample wrap selects * from the statement, and DuckDB renames a
// duplicate column of a subquery to name_1. The masked column keeps its
// index, so the query is served with the mask on it.
func TestExecute_PostQueryMaskUnderSampleWithDuplicateName(t *testing.T) {
	svc, _, _ := newHelloSluiceService(t, sampleWithFakeTenantPolicies)
	for _, sql := range []string{
		"SELECT o.tenant_id, c.tenant_id FROM shop.main.orders o JOIN shop.main.customers c ON c.id = o.customer_id",
		"SELECT * FROM shop.main.orders o JOIN shop.main.customers c ON c.id = o.customer_id",
	} {
		res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
			SQL: sql, User: helloUser(), Origin: queryservice.OriginREST,
		})
		if err != nil {
			t.Errorf("%s: %v", sql, err)
			continue
		}
		raw, masked := slices.IndexFunc(res.Columns, func(c executor.ColumnInfo) bool { return c.Name == "tenant_id" }),
			slices.IndexFunc(res.Columns, func(c executor.ColumnInfo) bool { return c.Name == "tenant_id_1" })
		rows := drainRows(t, res)
		if raw < 0 || masked < 0 || len(rows) != 3 {
			t.Errorf("%s: columns = %v, %d rows, want tenant_id and tenant_id_1 in 3 rows", sql, res.Columns, len(rows))
			continue
		}
		for _, r := range rows {
			if r[raw] != "acme" || r[masked] == "acme" {
				t.Errorf("%s: row %v, want order tenant raw and customer tenant masked", sql, r)
			}
		}
	}
}

const maskTenantPolicy = `
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata:
  name: mask-tenant
  priority: 50
spec:
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["customers", "orders"]
          columns: ["tenant_id"]
  mask:
    type: "null"
`

// The row filter predicate is policy SQL. A mask on the filter column must
// not rewrite it, or the filter compares NULL and returns nothing.
func TestExecute_MaskOnTheFilterColumnKeepsTheFilter(t *testing.T) {
	svc, _, _ := newHelloSluiceService(t, maskTenantPolicy)
	for sql, want := range map[string]string{
		"SELECT id FROM shop.main.orders ORDER BY id":                                                                           "[[101] [102] [103]]",
		"SELECT id, tenant_id FROM shop.main.customers ORDER BY 1":                                                              "[[1 <nil>] [2 <nil>]]",
		"SELECT o.id, c.name FROM shop.main.orders o JOIN shop.main.customers c ON c.id = o.customer_id ORDER BY 1 LIMIT 1":     "[[101 Alice]]",
		"WITH o AS (SELECT id, tenant_id FROM shop.main.orders) SELECT c.name FROM shop.main.customers c, o ORDER BY 1 LIMIT 1": "[[Alice]]",
	} {
		res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
			SQL: sql, User: helloUser(), Origin: queryservice.OriginREST,
		})
		if err != nil {
			t.Errorf("%s: %v", sql, err)
			continue
		}
		if got := fmt.Sprint(drainRows(t, res)); got != want {
			t.Errorf("%s: rows = %s, want %s", sql, got, want)
		}
	}
}

// DuckDB reads shop.customers as catalog shop with its default schema. No
// policy written for shop.main.customers matches that spelling, so the
// service refuses it before policy evaluation.
func TestExecute_CatalogUsedAsSchemaIsRefused(t *testing.T) {
	svc, ex, a := newHelloSluiceService(t)
	for _, sql := range []string{
		"SELECT c2.id, c2.email, c2.tenant_id FROM shop.customers c2 WHERE EXISTS (SELECT 1 FROM shop.main.orders)",
		`SELECT count(*) FROM "SHOP".customers, shop.main.orders`,
		"SELECT (SELECT count(*) FROM shop.orders) AS n FROM shop.main.customers",
	} {
		before := len(a.all())
		res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
			SQL: sql, User: helloUser(), Origin: queryservice.OriginREST,
		})
		if err == nil {
			t.Errorf("%s: served %v, want ACL_REJECTED", sql, drainRows(t, res))
			continue
		}
		if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeACLRejected {
			t.Errorf("%s: code = %s, want ACL_REJECTED", sql, ae.Code)
		}
		if recs := a.all()[before:]; len(recs) != 1 || recs[0].Decision != "reject" {
			t.Errorf("%s: audit records = %+v, want one reject", sql, recs)
		}
	}
	if ex.calls != 0 {
		t.Fatalf("executor ran %d times, want 0", ex.calls)
	}
}

const maskAllColumnsPolicy = `
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata:
  name: mask-all
  priority: 40
spec:
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["customers", "orders"]
          columns: ["*"]
  mask:
    type: "null"
`

// A wildcard mask matches every name on both tables. A column that only
// one of them has is not ambiguous and must be masked, not refused.
func TestExecute_WildcardMaskOnJoinedTables(t *testing.T) {
	svc, _, _ := newHelloSluiceService(t, maskAllColumnsPolicy)
	// Cross joins: with every column masked a join condition compares NULLs.
	for sql, want := range map[string]string{
		"SELECT name, total_cents FROM shop.main.customers c, shop.main.orders o LIMIT 2": "[[<nil> <nil>] [<nil> <nil>]]",
		"SELECT count(*) AS n FROM shop.main.customers c, shop.main.orders o ORDER BY n":  "[[6]]",
	} {
		res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
			SQL: sql, User: helloUser(), Origin: queryservice.OriginREST,
		})
		if err != nil {
			t.Errorf("%s: %v", sql, err)
			continue
		}
		if got := fmt.Sprint(drainRows(t, res)); got != want {
			t.Errorf("%s: rows = %s, want %s", sql, got, want)
		}
	}
	// id exists in both tables: a real ambiguity, refused.
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{
		SQL: "SELECT tenant_id FROM shop.main.customers c JOIN shop.main.orders o USING (tenant_id)", User: helloUser(),
	})
	if ae := pkgerr.FromError(err); err == nil || ae.Code != pkgerr.CodeMaskContext {
		t.Errorf("ambiguous masked column: err = %v, want ERR_MASK_UNSUPPORTED_CONTEXT", err)
	}
}

// An allow admits only the tables it names. A table that no allow covers is
// denied even when it is joined to, or hidden in a subquery beside, an
// allowed table. admin_users has no allow in the hello policies.
func TestExecute_UnallowedTableIsDenied(t *testing.T) {
	svc, ex, _ := newHelloSluiceService(t)
	denied := []string{
		"SELECT a.username FROM shop.main.admin_users a, (SELECT 1 FROM shop.main.customers LIMIT 1) x",
		"SELECT a.username FROM shop.main.customers c JOIN shop.main.admin_users a ON a.id = c.id",
		"SELECT v.u FROM (VALUES ((SELECT string_agg(username, ',') FROM shop.main.admin_users))) v(u), (SELECT 1 FROM shop.main.customers LIMIT 1) x",
		"SELECT d.database_name FROM duckdb_databases() d, (SELECT 1 FROM shop.main.customers LIMIT 1) x",
	}
	for _, sql := range denied {
		res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
			SQL: sql, User: helloUser(), Origin: queryservice.OriginREST,
		})
		if err == nil {
			_ = res.Rows.Close()
			t.Errorf("%s: served, want ACL_DENIED/ACL_REJECTED", sql)
			continue
		}
		if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeACLDenied && ae.Code != pkgerr.CodeACLRejected {
			t.Errorf("%s: code = %s, want ACL_DENIED or ACL_REJECTED", sql, ae.Code)
		}
	}
	if ex.calls != 0 {
		t.Fatalf("executor ran %d times, want 0", ex.calls)
	}
	// The allowed tables on their own still work.
	res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
		SQL:  "SELECT c.name FROM shop.main.customers c JOIN shop.main.orders o ON o.customer_id = c.id LIMIT 1",
		User: helloUser(),
	})
	if err != nil {
		t.Fatalf("allowed join: %v", err)
	}
	_ = res.Rows.Close()
}

// A FROM alias that is a DuckDB join keyword is refused before execution:
// otherwise DuckDB restructures the join and the row filter and mask are
// skipped. (A pre-existing deparse divergence, closed at the parser.)
func TestExecute_JoinKeywordAliasRefused(t *testing.T) {
	svc, ex, _ := newHelloSluiceService(t)
	for _, sql := range []string{
		`SELECT unnamed_subquery.email FROM shop.main.customers "semi" JOIN shop.main.orders o ON true`,
		`SELECT unnamed_subquery.email FROM shop.main.customers "anti" JOIN shop.main.orders o ON false`,
	} {
		res, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: sql, User: helloUser()})
		if err == nil {
			_ = res.Rows.Close()
			t.Errorf("%s: served, want ERR_SYNTAX", sql)
			continue
		}
		if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeSyntax {
			t.Errorf("%s: code = %s, want ERR_SYNTAX", sql, ae.Code)
		}
	}
	if ex.calls != 0 {
		t.Fatalf("executor ran %d times, want 0", ex.calls)
	}
}

// pg_query deparses AS qualify(x) bare, and DuckDB reads it as QUALIFY (x):
// a row predicate on the raw masked column or on the select alias of a
// post-masked item.
func TestExecute_QualifyAliasRefused(t *testing.T) {
	svc, ex, _ := newHelloSluiceService(t, fakeNamePolicy)
	for _, sql := range []string{
		"SELECT c.id, count(*) OVER () AS n FROM shop.main.customers c, generate_series(1, 1) AS qualify(email)",
		"SELECT c.id, count(*) OVER () AS n FROM shop.main.customers c CROSS JOIN generate_series(1, 1) AS qualify(email)",
		"SELECT c.name AS x, count(*) OVER () AS n FROM shop.main.customers c, (SELECT 1) AS qualify(x)",
	} {
		res, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: sql, User: helloUser()})
		if err == nil {
			t.Errorf("%s: served %v, want ERR_SYNTAX", sql, drainRows(t, res))
			continue
		}
		// After a parse error policy runs on the regex fallback, which
		// reads generate_series after JOIN as a table and denies it.
		if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeSyntax && ae.Code != pkgerr.CodeACLDenied {
			t.Errorf("%s: code = %s (%s), want ERR_SYNTAX or ACL_DENIED", sql, ae.Code, ae.Message)
		}
	}
	if ex.calls != 0 {
		t.Fatalf("executor ran %d times, want 0", ex.calls)
	}
}

const longNamePolicies = `
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: allow-payroll
  priority: 100
spec:
  effect: allow
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["payroll*"]
---
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: deny-payroll-snapshot
  priority: 200
spec:
  effect: deny
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["payroll_salaries_by_employee_and_month_from_hr_system_snapshot_2026"]
`

// pg_query cuts a 67-byte table name to 63 bytes and DuckDB does not, so a
// deny on the full name did not match and the statement passed through.
func TestExecute_LongIdentifierRefused(t *testing.T) {
	svc, ex, _ := newHelloSluiceService(t, longNamePolicies)
	sql := "SELECT * FROM shop.main.payroll_salaries_by_employee_and_month_from_hr_system_snapshot_2026"
	res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
		SQL: sql, User: helloUser(), Origin: queryservice.OriginREST,
	})
	if err == nil {
		t.Fatalf("served %v, want a refusal", drainRows(t, res))
	}
	if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeACLDenied {
		t.Errorf("code = %s (%s), want ACL_DENIED", ae.Code, ae.Message)
	}
	if ex.calls != 0 {
		t.Fatalf("executor ran %d times, want 0", ex.calls)
	}
}
