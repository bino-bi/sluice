// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/internal/schema"
	pkgds "github.com/bino-bi/sluice/pkg/datasource"
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
	svc, _, _ := newHelloSluiceService(t)

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
		{"select alias behind the alias prefix", "SELECT c.email AS e, alias.e AS m FROM shop.main.customers c"},
		{"set operation", "SELECT email FROM shop.main.customers UNION ALL SELECT email FROM shop.main.customers"},
		{"correlated subquery without FROM", "SELECT (SELECT c.email) AS e FROM shop.main.customers c"},
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
		{"masked column in expressions", "SELECT email || '' AS e, lower(email) AS l FROM shop.main.customers", "[[<nil> <nil>] [<nil> <nil>]]"},
		{"masked column under its own name in a column alias list", "SELECT c.email FROM shop.main.customers AS c(id, tenant_id, email)", "[[<nil>] [<nil>]]"},
		{"masked column aggregated in a scalar subquery", "SELECT (SELECT string_agg(email, ',') FROM shop.main.customers) AS emails", "[[<nil>]]"},
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

// A masked table without a row filter is wrapped too. The wrapper is named
// after the table, so a schema-qualified name no longer names the table or
// its row. The whole row and #2 are served masked
// (TestExecute_FormerMaskRefusalsServeMaskedValues).
func TestExecute_MaskOnTableWithoutRowFilter(t *testing.T) {
	svc, _, _ := newHelloSluiceService(t, adminUsersPolicies)

	// DuckDB cannot bind the name, so these fail at run time.
	for _, sql := range []string{
		"SELECT main.admin_users FROM shop.main.admin_users",
		"SELECT shop.main.admin_users FROM shop.main.admin_users",
		`SELECT "MAIN".admin_users FROM shop.main.admin_users`,
		"SELECT to_json(main.admin_users) AS j FROM shop.main.admin_users",
		"SELECT (main.admin_users).username AS u FROM shop.main.admin_users",
		"SELECT q.x FROM (SELECT main.admin_users AS x FROM shop.main.admin_users) q",
		"SELECT id FROM shop.main.admin_users WHERE CAST(main.admin_users AS VARCHAR) LIKE '%root%'",
	} {
		if rows, err := runRows(svc, sql); err == nil || len(rows) > 0 {
			t.Errorf("%s: served %v (err %v), want an error and no rows", sql, rows, err)
		}
	}

	for _, sql := range []string{
		"SELECT main.admin_users.username FROM shop.main.admin_users ORDER BY id",
		"SELECT shop.main.admin_users.username FROM shop.main.admin_users ORDER BY 1",
	} {
		rows, err := runRows(svc, sql)
		if err == nil {
			t.Errorf("%s: served %v, want ERR_UNSUPPORTED_SYNTAX", sql, rows)
			continue
		}
		if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeUnsupportedSyntax {
			t.Errorf("%s: code = %s (%s), want ERR_UNSUPPORTED_SYNTAX", sql, ae.Code, ae.Message)
		}
	}

	for sql, want := range map[string]string{
		"SELECT id, username, role FROM shop.main.admin_users ORDER BY id": "[[1 <nil> superuser] [2 <nil> maintainer]]",
		"SELECT a.username FROM shop.main.admin_users a ORDER BY a.id":     "[[<nil>] [<nil>]]",
		"SELECT * FROM shop.main.admin_users ORDER BY id LIMIT 1":          "[[1 <nil> superuser]]",
	} {
		rows, err := runRows(svc, sql)
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

// peopleSeed has a STRUCT column with a post-query mask (info) and one
// without a mask (addr).
const peopleSeed = `
CREATE TABLE people (id INTEGER, info STRUCT(ssn VARCHAR, city VARCHAR), addr STRUCT(city VARCHAR, zip VARCHAR));
INSERT INTO people VALUES
	(1, {'ssn': '111-22-3333', 'city': 'Berlin'}, {'city': 'Berlin', 'zip': '10115'}),
	(2, {'ssn': '999-88-7777', 'city': 'Paris'}, {'city': 'Paris', 'zip': '75001'});
`

const peoplePolicies = `
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: allow-people
  priority: 100
spec:
  effect: allow
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["people"]
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata:
  name: fake-people-info
  priority: 50
spec:
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["people"]
          columns: ["info"]
  mask:
    type: fake
    args:
      fakeType: name
`

// The wrapper passes a post-query masked column through raw, and the mask
// is applied in Go to a bare select item. A form that carries the column
// another way must stay refused for a table with a post-query mask.
func TestExecute_PostQueryMaskKeepsRefusals(t *testing.T) {
	svc, ex, _ := newSeededHelloSluiceService(t, nil, peopleSeed, fakeNamePolicy, peoplePolicies)
	for _, sql := range []string{
		"SELECT c FROM shop.main.customers c",
		"SELECT to_json(c) AS j FROM shop.main.customers c",
		"SELECT (c).name AS n FROM shop.main.customers c",
		"SELECT c['name'] AS n FROM shop.main.customers c",
		"SELECT id FROM shop.main.customers c WHERE CAST(c AS VARCHAR) LIKE '%Alice%'",
		"SELECT #2 FROM shop.main.customers",
		"SELECT c.* LIKE '%Alice%' FROM shop.main.customers c",
		"SELECT unnest(c) FROM shop.main.customers c",
		"SELECT struct_extract(c, 'name') AS n FROM shop.main.customers c",
		"SELECT max(c) FROM shop.main.customers c",
		"SELECT to_json(c.*) AS j FROM shop.main.customers c",
		"SELECT row(c.*) AS r FROM shop.main.customers c",
		"SELECT list_transform(ARRAY[c], x -> x.name) AS n FROM shop.main.customers c",
		// A star that names a table of an outer SELECT, and a star without FROM.
		"SELECT x.name FROM shop.main.orders o, shop.main.customers c, LATERAL (SELECT c.*) x",
		"SELECT c.id, (SELECT c.* FROM shop.main.orders o LIMIT 1) AS n FROM shop.main.customers c",
		"SELECT c.id FROM shop.main.customers c WHERE EXISTS (SELECT 1 FROM (SELECT c.* FROM shop.main.orders o) x WHERE x.name = 'Alice')",
		"SELECT x.name FROM shop.main.customers c, LATERAL (SELECT *) x",
		"SELECT c.n FROM shop.main.customers AS c(i, n)",
		"SELECT j.name FROM (shop.main.customers c JOIN shop.main.orders o ON o.customer_id = c.id) AS j",
		"SELECT (SELECT c.name FROM shop.main.orders c LIMIT 1) AS n FROM shop.main.customers c",
		// Two FROM items under one name.
		"SELECT upper(c.name) AS n FROM shop.main.customers c, shop.main.orders c LIMIT 3",
		"SELECT c.name AS n FROM shop.main.customers c JOIN shop.main.orders c ON true LIMIT 3",
		// Field access into the masked column.
		"SELECT c.name.x AS n FROM shop.main.customers c",
		"SELECT p.id, p.info.ssn FROM shop.main.people p",
		"SELECT id, info.ssn FROM shop.main.people",
		"SELECT info.ssn FROM shop.main.people info",
		"SELECT id FROM shop.main.people WHERE info.ssn LIKE '111%'",
		// DuckDB reads alias.<name> as the select alias <name>.
		"SELECT c.id, c.name AS n, alias.n AS m FROM shop.main.customers c ORDER BY c.id",
		"SELECT c.id, c.name AS n, upper(alias.n) AS m FROM shop.main.customers c ORDER BY c.id",
		"SELECT c.id, c.name AS n, (SELECT alias.n) AS m FROM shop.main.customers c ORDER BY c.id",
		"SELECT c.id, c.name AS n FROM shop.main.customers c WHERE alias.n = 'Alice'",
		"SELECT c.id, c.name AS n FROM shop.main.customers c ORDER BY alias.n DESC",
		"SELECT c.name AS n, count(*) AS k FROM shop.main.customers c GROUP BY alias.n",
		`SELECT c.id, c.name AS n, "ALIAS".n AS m FROM shop.main.customers c ORDER BY c.id`,
		// A FROM item called alias does not hide the select alias.
		"SELECT c.id, c.name AS n, alias.n AS m FROM shop.main.customers c, (SELECT 1 AS one) alias ORDER BY c.id",
		// The prefix is refused for every item, masked or not.
		"SELECT c.id AS i, alias.i AS j FROM shop.main.customers c",
		// A qualifier that is no FROM item: a schema, a catalog, a STRUCT
		// column, the select alias of a masked STRUCT item.
		"SELECT main.customers.name FROM shop.main.customers",
		"SELECT shop.main.customers.name FROM shop.main.customers",
		"SELECT main.orders.id FROM shop.main.orders WHERE EXISTS (SELECT 1 FROM shop.main.customers)",
		"SELECT id, addr.city FROM shop.main.people",
		"SELECT p.id, p.info AS s, s.ssn AS leak FROM shop.main.people p ORDER BY p.id",
		"SELECT c.id, (SELECT unknown.total_cents FROM shop.main.orders o LIMIT 1) AS t FROM shop.main.customers c",
		// A CTE body does not see the FROM items of the SELECT that defines it.
		"WITH q AS (SELECT addr.city AS s FROM shop.main.people) SELECT q.s FROM shop.main.orders addr, q",
		// A column alias list or a join alias, also on a table without a mask.
		"SELECT * FROM shop.main.orders AS o(customer_id, id) WHERE EXISTS (SELECT 1 FROM shop.main.customers) ORDER BY 1 LIMIT 1",
		"SELECT * FROM shop.main.orders AS o(a, b), shop.main.customers c WHERE c.id = 1",
		"SELECT * FROM (shop.main.orders o JOIN shop.main.orders o2 ON o2.id = o.id) AS j WHERE EXISTS (SELECT 1 FROM shop.main.customers)",
		// The schema cache has no entry for a CTE reference, so its star stays.
		"WITH q AS (SELECT id FROM shop.main.orders) SELECT * FROM q WHERE EXISTS (SELECT 1 FROM shop.main.customers)",
		"WITH q AS (SELECT id FROM shop.main.orders) SELECT c.id, q.* FROM shop.main.customers c, q",
	} {
		before := ex.calls
		rows, err := runRows(svc, sql)
		if err == nil {
			t.Errorf("%s: served %v, want ERR_MASK_UNSUPPORTED_CONTEXT", sql, rows)
			continue
		}
		if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeMaskContext {
			t.Errorf("%s: code = %s (%s), want ERR_MASK_UNSUPPORTED_CONTEXT", sql, ae.Code, ae.Message)
		}
		if ex.calls != before {
			t.Errorf("%s: executor was reached", sql)
		}
	}

	// A table alias as qualifier and (col).field stay served, and so do the
	// stars that star expansion keeps or drops.
	for sql, want := range map[string]string{
		"SELECT c.id, o.total_cents FROM shop.main.customers c JOIN shop.main.orders o ON o.customer_id = c.id ORDER BY o.id":                                            "[[1 12900] [1 4500] [2 19900]]",
		"SELECT customers.id, q.n FROM shop.main.customers, (SELECT count(*) AS n FROM shop.main.orders o) q ORDER BY 1":                                                 "[[1 3] [2 3]]",
		"SELECT c.id, g.i FROM shop.main.customers c, generate_series(1, 1) AS g(i) ORDER BY c.id":                                                                       "[[1 1] [2 1]]",
		"SELECT p.id, (p.addr).city AS city FROM shop.main.people p ORDER BY p.id":                                                                                       "[[1 Berlin] [2 Paris]]",
		"SELECT id, (addr).city AS city FROM shop.main.people ORDER BY id":                                                                                               "[[1 Berlin] [2 Paris]]",
		"SELECT count(*) FROM shop.main.orders o WHERE EXISTS (SELECT * FROM shop.main.customers c WHERE c.id = o.customer_id)":                                          "[[3]]",
		"SELECT c.id FROM shop.main.customers c WHERE c.id IN (SELECT q.* FROM (SELECT customer_id FROM shop.main.orders) q) ORDER BY c.id":                              "[[1] [2]]",
		"SELECT c.id FROM shop.main.customers c WHERE c.id IN (SELECT c.* FROM (SELECT customer_id FROM shop.main.orders) c) ORDER BY c.id":                              "[[1] [2]]",
		"WITH q AS (SELECT customer_id FROM shop.main.orders) SELECT c.id FROM shop.main.customers c WHERE c.id IN (SELECT q.* FROM q) ORDER BY c.id":                    "[[1] [2]]",
		"SELECT count(*) FROM shop.main.orders o WHERE o.customer_id IN (SELECT q.cid FROM (SELECT x.*, c.id AS cid FROM shop.main.customers c, (SELECT 1 AS one) x) q)": "[[3]]",
	} {
		rows, err := runRows(svc, sql)
		if err != nil {
			t.Errorf("%s: %v", sql, err)
			continue
		}
		if got := fmt.Sprint(rows); got != want {
			t.Errorf("%s: rows = %s, want %s", sql, got, want)
		}
	}
}

const productsSeed = `
CREATE TABLE products (id INTEGER, name TEXT, tenant_id TEXT);
INSERT INTO products VALUES (1, 'Widget', 'acme'), (2, 'Gadget', 'acme');
`

// products is a second masked table with a column called name.
const productsPolicies = `
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: allow-products
  priority: 100
spec:
  effect: allow
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["products"]
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata:
  name: mask-product-id
  priority: 50
spec:
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["products"]
          columns: ["id"]
  mask:
    type: "null"
`

// A join condition does not see the FROM items that follow it, so DuckDB
// binds a name there to an outer table: the post-query masked column, or
// the whole row under the table's alias. A later FROM item that has the
// name as a column must not make the rewriter take it as bound there.
func TestExecute_PostQueryMaskNameBoundOutward(t *testing.T) {
	svc, ex, _ := newSeededHelloSluiceService(t, nil, productsSeed, fakeNamePolicy, productsPolicies)
	const (
		laterJoin = "FROM shop.main.orders o JOIN shop.main.orders o2 ON o2.id = o.id AND name = 'Alice' JOIN shop.main.products p ON true"
		wholeRow  = "FROM range(1) a(i) JOIN range(1) b(j) ON to_json(placed_at)::VARCHAR LIKE '%Alice%' JOIN shop.main.orders o ON true"
	)
	for _, sql := range []string{
		"SELECT c.id, (SELECT count(*) " + laterJoin + ") AS k FROM shop.main.customers c ORDER BY c.id",
		"SELECT c.id FROM shop.main.customers c WHERE EXISTS (SELECT 1 " + laterJoin + ") ORDER BY c.id",
		"SELECT c.id, (SELECT count(*) FROM shop.main.orders o JOIN shop.main.orders o2 ON o2.id = o.id AND name = 'Alice', shop.main.products p) AS k FROM shop.main.customers c ORDER BY c.id",
		"SELECT c.id, (SELECT string_agg(g.ch, '' ORDER BY g.i) FROM (SELECT 1 AS one) x JOIN (SELECT a.i, chr(b.j::INT) AS ch FROM generate_series(1, 40) a(i), generate_series(32, 126) b(j)) g ON substr(name, g.i, 1) = g.ch LEFT JOIN shop.main.products p ON false) AS leaked FROM shop.main.customers c ORDER BY c.id",
		// DuckDB binds a name in LIMIT outward too.
		"SELECT c.id, (SELECT p.tenant_id FROM shop.main.products p ORDER BY p.tenant_id LIMIT length(name)) AS k FROM shop.main.customers c ORDER BY c.id",
		// The outer alias is a column of orders.
		"SELECT placed_at.id, (SELECT count(*) " + wholeRow + ") AS n FROM shop.main.customers placed_at ORDER BY 1",
		"SELECT placed_at.id, (SELECT string_agg(chr(b.j::INT), '' ORDER BY p.k) FROM range(1, 30) p(k) JOIN range(32, 127) b(j) ON ascii(substr((placed_at).name, p.k::INT, 1)) = b.j JOIN shop.main.orders o ON o.id = 101) AS n FROM shop.main.customers placed_at ORDER BY 1",
		"SELECT placed_at.id, string_agg(chr(b.j::INT), '' ORDER BY p.k) AS n FROM shop.main.customers placed_at CROSS JOIN range(1, 30) p(k) JOIN range(32, 127) b(j) ON ascii(substr((placed_at).name, p.k::INT, 1)) = b.j JOIN shop.main.orders o ON o.id = 101 GROUP BY 1 ORDER BY 1",
		"SELECT placed_at.id, (SELECT o.id FROM shop.main.orders o ORDER BY o.id LIMIT length(to_json(placed_at)::VARCHAR)) AS k FROM shop.main.customers placed_at ORDER BY 1",
		"SELECT o.placed_at FROM shop.main.orders o JOIN shop.main.customers placed_at ON placed_at.id = o.customer_id",
		// main is a later FROM item with a column called customers.
		"SELECT customers.id, (SELECT count(*) FROM range(1) a(i) JOIN range(1) b(j) ON to_json(main.customers)::VARCHAR LIKE '%Alice%' JOIN (SELECT 1 AS customers) main ON true) AS n FROM shop.main.customers ORDER BY 1",
	} {
		before := ex.calls
		rows, err := runRows(svc, sql)
		if err == nil {
			t.Errorf("%s: served %v, want ERR_MASK_UNSUPPORTED_CONTEXT", sql, rows)
			continue
		}
		if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeMaskContext {
			t.Errorf("%s: code = %s (%s), want ERR_MASK_UNSUPPORTED_CONTEXT", sql, ae.Code, ae.Message)
		}
		if ex.calls != before {
			t.Errorf("%s: executor was reached", sql)
		}
	}

	// A qualified name says which table is meant.
	for sql, want := range map[string]string{
		"SELECT c.id, (SELECT count(*) FROM shop.main.orders o JOIN shop.main.products p ON p.name = 'Widget') AS k FROM shop.main.customers c ORDER BY c.id": "[[1 3] [2 3]]",
		"SELECT c.id, (SELECT max(p.name) FROM shop.main.products p) AS k FROM shop.main.customers c ORDER BY c.id":                                           "[[1 Widget] [2 Widget]]",
		"SELECT x.id FROM shop.main.customers x JOIN shop.main.orders o ON o.customer_id = x.id AND o.placed_at IS NOT NULL ORDER BY o.id":                    "[[1] [1] [2]]",
	} {
		rows, err := runRows(svc, sql)
		if err != nil {
			t.Errorf("%s: %v", sql, err)
			continue
		}
		if got := fmt.Sprint(rows); got != want {
			t.Errorf("%s: rows = %s, want %s", sql, got, want)
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
	// USING merges tenant_id of both tables. Both sides are masked, so the
	// join compares NULLs and matches nothing.
	rows, err := runRows(svc, "SELECT tenant_id FROM shop.main.customers c JOIN shop.main.orders o USING (tenant_id)")
	if err != nil || len(rows) > 0 {
		t.Errorf("USING on a masked column: rows = %v, err = %v, want no rows", rows, err)
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

// The deparse drops the parentheses of (a OR b) IS NULL, and DuckDB reads
// a OR (b IS NULL). The parse check refuses the statement before it runs.
func TestExecute_RewriteKeepsDuckDBMeaning(t *testing.T) {
	svc, ex, a := newHelloSluiceService(t)
	sql := "SELECT id FROM shop.main.customers WHERE (tenant_id = 'x' OR id = 1) IS NULL"
	res, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: sql, User: helloUser()})
	if err == nil {
		t.Fatalf("served %v, want ERR_UNSUPPORTED_SYNTAX", drainRows(t, res))
	}
	if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeUnsupportedSyntax {
		t.Errorf("code = %s (%s), want ERR_UNSUPPORTED_SYNTAX", ae.Code, ae.Message)
	}
	if ex.calls != 0 {
		t.Fatalf("executor ran %d times, want 0", ex.calls)
	}
	recs := a.all()
	if len(recs) != 1 || recs[0].Decision != audit.DecisionError || recs[0].ErrorCode != string(pkgerr.CodeUnsupportedSyntax) {
		t.Errorf("audit records = %+v, want one error with ERR_UNSUPPORTED_SYNTAX", recs)
	}
}

// Forms the rewriter normalizes for DuckDB are served with the rows and
// names DuckDB gives the caller's text. Each one runs under the tenant
// filter, so it takes the rewrite path.
func TestExecute_RewriteNormalizedForDuckDB(t *testing.T) {
	svc, _, _ := newHelloSluiceService(t)
	cases := []struct {
		name, sql, column string
		want              string
	}{
		// 2 days back keeps Alice and Bob; 2 seconds back keeps Bob alone.
		{"interval literal", "SELECT count(*) AS n FROM shop.main.customers WHERE created_at::timestamp > TIMESTAMP '2026-01-04 00:00:00' - INTERVAL '2' DAY", "n", "[[int64:2]]"},
		{"like escape", "SELECT name FROM shop.main.customers WHERE name LIKE 'A%' ESCAPE '!' OR name NOT ILIKE '%b%' ESCAPE '!'", "name", "[[string:Alice]]"},
		{"bare float", "SELECT CAST(id AS float) AS f FROM shop.main.customers ORDER BY id", "f", "[[float32:1] [float32:2]]"},
		{"alias spelling", "SELECT id AS CustomerId FROM shop.main.customers ORDER BY CustomerId", "CustomerId", "[[int32:1] [int32:2]]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: tc.sql, User: helloUser()})
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if len(res.Columns) != 1 || res.Columns[0].Name != tc.column {
				t.Errorf("columns = %+v, want %q", res.Columns, tc.column)
			}
			var got []string
			for _, row := range drainRows(t, res) {
				var cells []string
				for _, v := range row {
					cells = append(cells, fmt.Sprintf("%T:%v", v, v))
				}
				got = append(got, "["+strings.Join(cells, " ")+"]")
			}
			if s := "[" + strings.Join(got, " ") + "]"; s != tc.want {
				t.Errorf("rows = %s, want %s", s, tc.want)
			}
		})
	}
}

const saltedNamePolicy = `
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata:
  name: hash-name
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
    type: hash
    args:
      algorithm: sha256
      saltRef: secret://env/SLUICE_TEST_NAME_SALT
`

// The rewriter binds its own values from $1: the filter value first, then
// the mask params. A $n in the caller's text would read them, the salt of
// a hash mask too. Caller params are never bound, so $n is refused.
func TestExecute_CallerParamsRefused(t *testing.T) {
	const salt = "TopSecretSalt123"
	t.Setenv("SLUICE_TEST_NAME_SALT", salt)
	svc, ex, a := newHelloSluiceService(t, saltedNamePolicy)
	for _, sql := range []string{
		"SELECT $2 AS s, name FROM shop.main.customers",
		"SELECT $1 AS p, id FROM shop.main.customers",
		"SELECT id FROM shop.main.orders WHERE (SELECT $1) IS NOT NULL",
	} {
		res, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: sql, User: helloUser()})
		if err == nil {
			t.Errorf("%s: served %v, want ERR_UNSUPPORTED_SYNTAX", sql, drainRows(t, res))
			continue
		}
		if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeUnsupportedSyntax || !strings.Contains(ae.Message, "query parameters") {
			t.Errorf("%s: code = %s (%s), want ERR_UNSUPPORTED_SYNTAX", sql, ae.Code, ae.Message)
		}
	}
	if ex.calls != 0 {
		t.Fatalf("executor ran %d times, want 0", ex.calls)
	}
	for _, rec := range a.all() {
		if rec.Decision != audit.DecisionError || rec.ErrorCode != string(pkgerr.CodeUnsupportedSyntax) {
			t.Errorf("audit decision %s, error_code %q, want error and ERR_UNSUPPORTED_SYNTAX", rec.Decision, rec.ErrorCode)
		}
	}
}

// runRows executes sql and returns its rows, also the rows read before an
// error stopped it.
func runRows(svc *queryservice.Service, sql string) ([][]any, error) {
	res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
		SQL: sql, User: helloUser(), Origin: queryservice.OriginREST,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Rows.Close() }()
	var out [][]any
	for res.Rows.Next() {
		row := make([]any, len(res.Columns))
		dest := make([]any, len(row))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := res.Rows.Scan(dest...); err != nil {
			return out, err
		}
		out = append(out, row)
	}
	return out, res.Rows.Err()
}

const nullTotalCentsPolicy = `
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata:
  name: null-total-cents
  priority: 50
spec:
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["orders"]
          columns: ["total_cents"]
  mask:
    type: "null"
`

const constantTotalCentsPolicy = `
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata:
  name: constant-total-cents
  priority: 50
spec:
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["orders"]
          columns: ["total_cents"]
  mask:
    type: constant
    args:
      value: 2
`

// constantEmailPolicy outranks the shipped null mask on email.
const constantEmailPolicy = `
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata:
  name: constant-email
  priority: 60
spec:
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["customers"]
          columns: ["email"]
  mask:
    type: constant
    args:
      value: "***"
`

// USING and NATURAL name the join column with a string, not a column
// reference. A mask applied where a column is referenced does not reach
// them, so the join compared the raw values: the matched rows show the
// value, and the other side returns it. The join must compare masked
// values. A masked value equals none of the probe values, so no row may
// come back.
func TestExecute_MergedJoinOnMaskedColumnComparesMaskedValues(t *testing.T) {
	cases := []struct {
		name   string
		policy string
		sql    string
		// DuckDB types NULL AS email as INTEGER, so comparing it with a
		// string fails. That serves nothing either.
		errOK bool
	}{
		{"USING on a null-masked number", nullTotalCentsPolicy,
			"SELECT o.id, g.total_cents AS recovered FROM shop.main.orders o JOIN generate_series(0, 100000) AS g(total_cents) USING (total_cents) ORDER BY o.id", false},
		{"NATURAL on a null-masked number", nullTotalCentsPolicy,
			"SELECT o.id, g.total_cents AS recovered FROM shop.main.orders o NATURAL JOIN generate_series(0, 100000) AS g(total_cents) ORDER BY o.id", false},
		{"USING on the null-masked email", "",
			"SELECT c.id FROM shop.main.customers c JOIN (SELECT 'alice@acme.test' AS email) x USING (email)", true},
		{"NATURAL on the null-masked email", "",
			"SELECT c.id FROM shop.main.customers c NATURAL JOIN (SELECT 'alice@acme.test' AS email) x", true},
		{"USING on a constant-masked email", constantEmailPolicy,
			"SELECT x.email FROM shop.main.customers c JOIN (VALUES ('alice@acme.test'), ('bob@acme.test'), ('zz@x')) x(email) USING (email)", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var extra []string
			if tc.policy != "" {
				extra = append(extra, tc.policy)
			}
			svc, _, _ := newHelloSluiceService(t, extra...)
			rows, err := runRows(svc, tc.sql)
			if len(rows) > 0 {
				t.Fatalf("rows = %v, want none: the join compared raw values", rows)
			}
			if err != nil && !tc.errOK {
				t.Fatalf("execute: %v", err)
			}
		})
	}
}

// A constant mask is a literal. As a whole ORDER BY, GROUP BY or DISTINCT
// ON key DuckDB reads an integer literal as a select list position, so the
// key must be the masked column, not the literal.
func TestExecute_ConstantMaskSortKeyIsNotAPosition(t *testing.T) {
	svc, _, _ := newHelloSluiceService(t, constantTotalCentsPolicy)
	for sql, want := range map[string]string{
		"SELECT id, customer_id FROM shop.main.orders ORDER BY total_cents DESC, id":                      "[[101 1] [102 1] [103 2]]",
		"SELECT total_cents, count(*) AS n FROM shop.main.orders GROUP BY total_cents":                    "[[2 3]]",
		"SELECT DISTINCT ON (total_cents) total_cents, id FROM shop.main.orders ORDER BY total_cents, id": "[[2 101]]",
	} {
		rows, err := runRows(svc, sql)
		if err != nil {
			t.Errorf("%s: %v", sql, err)
			continue
		}
		if got := fmt.Sprint(rows); got != want {
			t.Errorf("%s: rows = %s, want %s", sql, got, want)
		}
	}
}

// A masked column in a sort, group, filter or join clause reads the mask
// output, or the query is refused with a code of its own. Each query probes
// with a raw value of the column: a clause that read the raw value would
// return other rows. ERR_INTERNAL is no answer.
func TestExecute_MaskedColumnInEveryClause(t *testing.T) {
	type clause struct{ name, sql string }
	clauses := func(col, probe string) []clause {
		const from = " FROM shop.main.customers"
		return []clause{
			// The reported query. Its row order is not defined.
			{"ORDER BY", "SELECT id" + from + " ORDER BY " + col},
			// A sort on the raw value would return 2 before 1.
			{"ORDER BY DESC", "SELECT id" + from + " ORDER BY " + col + " DESC, id"},
			{"GROUP BY", "SELECT " + col + ", count(*) AS n" + from + " GROUP BY " + col},
			{"WHERE =", "SELECT id" + from + " WHERE " + col + " = '" + probe + "'"},
			{"WHERE LIKE", "SELECT id" + from + " WHERE " + col + " LIKE '" + probe[:1] + "%'"},
			{"HAVING", "SELECT tenant_id" + from + " GROUP BY tenant_id HAVING min(" + col + ") = '" + probe + "'"},
			{"DISTINCT", "SELECT DISTINCT " + col + from},
			{"JOIN ON", "SELECT c.id" + from + " c JOIN (SELECT '" + probe + "' AS v) x ON c." + col + " = x.v"},
		}
	}

	masks := []struct {
		name       string
		policy     string // "" is the shipped null mask on email
		col, probe string
		// want holds the rows per clause, sorted here except for ORDER BY
		// DESC. Nil: every clause is refused with
		// ERR_MASK_UNSUPPORTED_CONTEXT before DuckDB runs.
		want map[string]string
	}{
		{"null", "", "email", "alice@acme.test", map[string]string{
			"ORDER BY": "[[1] [2]]", "ORDER BY DESC": "[[1] [2]]", "GROUP BY": "[[<nil> 2]]", "WHERE =": "[]",
			"WHERE LIKE": "[]", "HAVING": "[]", "DISTINCT": "[[<nil>]]", "JOIN ON": "[]",
		}},
		{"constant", constantEmailPolicy, "email", "alice@acme.test", map[string]string{
			"ORDER BY": "[[1] [2]]", "ORDER BY DESC": "[[1] [2]]", "GROUP BY": "[[*** 2]]", "WHERE =": "[]",
			"WHERE LIKE": "[]", "HAVING": "[]", "DISTINCT": "[[***]]", "JOIN ON": "[]",
		}},
		{"post-query", fakeNamePolicy, "name", "Alice", nil},
	}
	for _, m := range masks {
		var extra []string
		if m.policy != "" {
			extra = append(extra, m.policy)
		}
		svc, ex, _ := newHelloSluiceService(t, extra...)
		for _, c := range clauses(m.col, m.probe) {
			t.Run(m.name+"/"+c.name, func(t *testing.T) {
				before := ex.calls
				rows, err := runRows(svc, c.sql)
				if err != nil {
					ae := pkgerr.FromError(err)
					if m.want != nil || ae.Code != pkgerr.CodeMaskContext {
						t.Fatalf("code = %s, cause = %v", ae.Code, errors.Unwrap(ae))
					}
					if ex.calls != before {
						t.Fatal("executor was reached")
					}
					return
				}
				if m.want == nil {
					t.Fatalf("served %v, want ERR_MASK_UNSUPPORTED_CONTEXT", rows)
				}
				if c.name != "ORDER BY DESC" {
					slices.SortFunc(rows, func(a, b []any) int { return strings.Compare(fmt.Sprint(a), fmt.Sprint(b)) })
				}
				if got := fmt.Sprint(rows); got != m.want[c.name] {
					t.Fatalf("rows = %s, want %s", got, m.want[c.name])
				}
			})
		}
	}
}

// DuckDB refuses a statement that does not bind or convert, also when it
// is the mask output that does not fit. The client gets ERR_QUERY_FAILED
// with the generic message: DuckDB builds its text from the rewritten SQL
// and quotes values.
func TestExecute_StatementRefusedByDuckDB(t *testing.T) {
	cases := []struct{ name, policy, sql string }{
		{"unknown column", "", "SELECT nosuch FROM shop.main.customers"},
		// What rc.2 wrote for ORDER BY on the null-masked email.
		{"literal sort key", "", "SELECT id FROM shop.main.customers ORDER BY NULL"},
		{"cast of a raw value", "", "SELECT CAST(name AS INTEGER) AS n FROM shop.main.customers"},
		{"cast of a constant mask", constantEmailPolicy, "SELECT CAST(email AS INTEGER) AS n FROM shop.main.customers"},
		{"constant mask parsed as a date", constantEmailPolicy, "SELECT strptime(email, '%Y-%m-%d') AS d FROM shop.main.customers"},
		{"scalar subquery with two rows", "", "SELECT id FROM shop.main.customers WHERE id = (SELECT id FROM shop.main.customers)"},
		{"unknown type", "", "SELECT CAST(id AS nosuchtype) AS n FROM shop.main.customers"},
		{"USING on the null-masked email", "", "SELECT c.id FROM shop.main.customers c JOIN (SELECT 'alice@acme.test' AS email) x USING (email)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var extra []string
			if tc.policy != "" {
				extra = append(extra, tc.policy)
			}
			svc, _, a := newHelloSluiceService(t, extra...)
			rows, err := runRows(svc, tc.sql)
			if err == nil {
				t.Fatalf("served %v, want ERR_QUERY_FAILED", rows)
			}
			ae := pkgerr.FromError(err)
			if ae.Code != pkgerr.CodeQueryFailed || ae.Status() != 400 || ae.QueryID == "" {
				t.Fatalf("code = %s, status = %d, query_id = %q, cause = %v", ae.Code, ae.Status(), ae.QueryID, errors.Unwrap(ae))
			}
			if ae.Message != pkgerr.Message(pkgerr.CodeQueryFailed) {
				t.Fatalf("message = %q, want the generic one", ae.Message)
			}
			recs := a.all()
			if len(recs) == 0 || recs[len(recs)-1].ErrorCode != string(pkgerr.CodeQueryFailed) {
				t.Fatalf("audit records = %+v, want the client code on the last one", recs)
			}
		})
	}
}

const allowAdminUsersPolicy = `
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: allow-admin-users-only
  priority: 100
spec:
  effect: allow
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["admin_users"]
`

const filterAdminTenantPolicy = `
apiVersion: sluice.bino.bi/v1alpha1
kind: RowFilterPolicy
metadata:
  name: filter-admin-tenant
  priority: 80
spec:
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["admin_users"]
  combine: restrictive
  filter:
    predicate:
      column: tenant_id
      op: Equals
      value: "{{ subject.tenantId }}"
`

// admin_users has no tenant_id column, and the cache says it has one, as
// after the column was dropped at the source. DuckDB binds a filter column
// the table lacks to an outer FROM item or struct column with that name.
// The filter column is qualified with sluice_src, and a statement whose
// text holds that name is refused. So a name the caller writes cannot take
// the place of the table, and the filter fails closed. A name built at run
// time still can: the residual risk in THREAT_MODEL.md, pinned at the end.
func TestExecute_WrapperQualifierCannotBeCaptured(t *testing.T) {
	cache := schema.NewStatic([]*schema.Entry{{
		Key:     schema.Key{Catalog: "shop", Schema: "main", Table: "admin_users"},
		Columns: []pkgds.Column{{Name: "id"}, {Name: "username"}, {Name: "role"}, {Name: "tenant_id"}},
	}})
	for name, policies := range map[string][]string{
		"filter only":     {allowAdminUsersPolicy, filterAdminTenantPolicy},
		"filter and mask": {adminUsersPolicies, filterAdminTenantPolicy},
	} {
		t.Run(name, func(t *testing.T) {
			svc, ex, _ := newHelloSluiceServiceWithSchema(t, cache, policies...)
			for _, sql := range []string{
				"SELECT a.id, a.role FROM (SELECT 'acme' AS tenant_id) x, shop.main.admin_users a",
				"SELECT a.id, a.role FROM (SELECT 'acme' AS tenant_id) admin_users, shop.main.admin_users a",
				"SELECT a.id, a.role FROM (SELECT 'acme' AS tenant_id) admin_users JOIN shop.main.admin_users a ON true",
				"SELECT a.id, a.role FROM (SELECT struct_pack(tenant_id := 'acme') AS admin_users) s, shop.main.admin_users a",
				"SELECT (SELECT count(*) FROM shop.main.admin_users a) AS n FROM (SELECT 'acme' AS tenant_id) admin_users",
			} {
				rows, err := runRows(svc, sql)
				if err == nil || len(rows) > 0 {
					t.Errorf("%s: served %v (err %v), want an error and no rows", sql, rows, err)
				}
			}
			// The statement text may not hold the wrapper's own alias.
			for _, sql := range []string{
				"SELECT a.id, a.role FROM (SELECT 'acme' AS tenant_id) sluice_src, shop.main.admin_users a",
				`SELECT a.id, a.role FROM (SELECT 'acme' AS tenant_id) "SLUICE_SRC" JOIN shop.main.admin_users a ON true`,
				"SELECT a.id, a.role FROM (SELECT struct_pack(tenant_id := 'acme') AS sluice_src) s, shop.main.admin_users a",
				"WITH sluice_src AS (SELECT 'acme' AS tenant_id) SELECT a.id, a.role FROM sluice_src, shop.main.admin_users a",
				"SELECT sluice_src.id FROM shop.main.admin_users sluice_src",
			} {
				before := ex.calls
				rows, err := runRows(svc, sql)
				if err == nil {
					t.Errorf("%s: served %v, want ERR_UNSUPPORTED_SYNTAX", sql, rows)
					continue
				}
				if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeUnsupportedSyntax {
					t.Errorf("%s: code = %s (%s), want ERR_UNSUPPORTED_SYNTAX", sql, ae.Code, ae.Message)
				}
				if ex.calls != before {
					t.Errorf("%s: executor was reached", sql)
				}
			}
			// Residual risk: the check reads the statement text, and this
			// struct key only exists at run time. The filter then tests the
			// caller's value and every row is served.
			const runTime = `SELECT a.id, a.role FROM (SELECT unnest(from_json('{"sluice_' || 'src":{"tenant_id":"acme"}}', ` +
				`'{"sluice_' || 'src":{"tenant_id":"VARCHAR"}}'))) s, shop.main.admin_users a ORDER BY a.id`
			rows, err := runRows(svc, runTime)
			if got := fmt.Sprint(rows); err != nil || got != "[[1 superuser] [2 maintainer]]" {
				t.Errorf("run-time name: rows = %s, err = %v, want the unfiltered rows; when this changes, update the residual risk in THREAT_MODEL.md", got, err)
			}
		})
	}
}

// The cache entry of customers predates the email column. The mask is on
// email, so the column must stay hidden or fail, never come back raw.
func TestExecute_StaleSchemaFailsClosed(t *testing.T) {
	cache := schema.NewStatic([]*schema.Entry{{
		Key:     schema.Key{Catalog: "shop", Schema: "main", Table: "customers"},
		Columns: []pkgds.Column{{Name: "id"}, {Name: "name"}, {Name: "tenant_id"}, {Name: "created_at"}},
	}})
	svc, _, _ := newHelloSluiceServiceWithSchema(t, cache)
	for _, sql := range []string{
		"SELECT email FROM shop.main.customers",
		"SELECT id, email FROM shop.main.customers WHERE email LIKE '%@%'",
		"SELECT c.email FROM shop.main.customers c",
		"SELECT * FROM shop.main.customers",
		"SELECT q.email FROM (SELECT * FROM shop.main.customers) q",
		"SELECT c FROM shop.main.customers c",
		"SELECT to_json(c) AS j FROM shop.main.customers c",
	} {
		rows, err := runRows(svc, sql)
		if got := fmt.Sprint(rows); strings.Contains(got, "@") {
			t.Errorf("%s: unmasked email in result %s (err %v)", sql, got, err)
		}
	}
}

const keywordColumnsSeed = `
CREATE TABLE kw (id INTEGER, "current_date" TEXT, "localtime" TEXT, "current_timestamp" TEXT);
INSERT INTO kw VALUES (1, 'SECRET-A', 'SECRET-L1', 'SECRET-T1'), (2, 'SECRET-B', 'SECRET-L2', 'SECRET-T2');
`

const keywordColumnsPolicies = `
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: allow-kw
  priority: 100
spec:
  effect: allow
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["kw"]
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata:
  name: fake-kw
  priority: 50
spec:
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["kw"]
          columns: ["current_date", "localtime", "current_timestamp"]
  mask:
    type: fake
    args:
      fakeType: name
`

// pg_query parses a bare CURRENT_DATE, LOCALTIME or CURRENT_TIMESTAMP as a
// function, not as a column reference. DuckDB binds the word to a column,
// a select alias or a table of that name first. With a post-query mask
// behind the name, the word is refused like the column reference.
func TestExecute_PostQueryMaskBehindKeywordName(t *testing.T) {
	svc, ex, _ := newSeededHelloSluiceService(t, nil, keywordColumnsSeed, fakeNamePolicy, keywordColumnsPolicies)
	for _, sql := range []string{
		// A post-query masked column with the name of the keyword.
		"SELECT id, current_date AS d FROM shop.main.kw ORDER BY id",
		"SELECT id, CAST(current_date AS VARCHAR) AS d FROM shop.main.kw",
		"SELECT id, concat(current_timestamp, '') AS d FROM shop.main.kw",
		"SELECT id, lower(localtime) AS d FROM shop.main.kw",
		"SELECT id FROM shop.main.kw WHERE CAST(current_date AS VARCHAR) LIKE 'SECRET-A%'",
		"SELECT id FROM shop.main.kw ORDER BY localtime DESC",
		"SELECT count(*) AS n FROM shop.main.kw GROUP BY localtime",
		"SELECT k.id FROM shop.main.kw k JOIN shop.main.orders o ON CAST(current_date AS VARCHAR) LIKE 'SECRET-A%'",
		// The select alias of a bound item.
		"SELECT c.id, c.name AS localtime, localtime AS leak FROM shop.main.customers c",
		"SELECT c.id, c.name AS current_date FROM shop.main.customers c ORDER BY current_date DESC",
		"SELECT c.id, c.name AS current_date FROM shop.main.customers c WHERE CAST(current_date AS VARCHAR) LIKE 'Al%'",
		// The alias of a table with a post-query mask: the whole row.
		`SELECT current_date FROM shop.main.customers "current_date"`,
		`SELECT CAST(localtime AS VARCHAR) AS j FROM shop.main.customers "localtime"`,
	} {
		before := ex.calls
		rows, err := runRows(svc, sql)
		if err == nil {
			t.Errorf("%s: served %v, want ERR_MASK_UNSUPPORTED_CONTEXT", sql, rows)
			continue
		}
		if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeMaskContext {
			t.Errorf("%s: code = %s (%s), want ERR_MASK_UNSUPPORTED_CONTEXT", sql, ae.Code, ae.Message)
		}
		if ex.calls != before {
			t.Errorf("%s: executor was reached", sql)
		}
	}

	// The quoted name is a column reference and gets the mask.
	rows, err := runRows(svc, `SELECT id, "current_date", "localtime" FROM shop.main.kw ORDER BY id`)
	if got := fmt.Sprint(rows); err != nil || len(rows) != 2 || strings.Contains(got, "SECRET") {
		t.Errorf("quoted names: rows = %s, err = %v, want two masked rows", got, err)
	}
	// Without a masked name behind it the keyword stays the function.
	rows, err = runRows(svc, "SELECT id, current_date > DATE '2000-01-01' AS d FROM shop.main.customers ORDER BY id")
	if got := fmt.Sprint(rows); err != nil || got != "[[1 true] [2 true]]" {
		t.Errorf("function: rows = %s, err = %v, want [[1 true] [2 true]]", got, err)
	}
}

// The cache entry of orders still lists ghost, a column the source no
// longer has. Star expansion writes alias.ghost for it, and DuckDB reads
// that as the select alias ghost: the raw value of the post-query masked
// item, at a position no mask is bound to. A table called alias is refused.
func TestExecute_PostQueryMaskTableNamedAliasRefused(t *testing.T) {
	cache := schema.NewStatic([]*schema.Entry{{
		Key:     schema.Key{Catalog: "shop", Schema: "main", Table: "customers"},
		Columns: []pkgds.Column{{Name: "id"}, {Name: "name"}, {Name: "email"}, {Name: "tenant_id"}, {Name: "created_at"}},
	}, {
		Key: schema.Key{Catalog: "shop", Schema: "main", Table: "orders"},
		Columns: []pkgds.Column{{Name: "id"}, {Name: "customer_id"}, {Name: "total_cents"}, {Name: "tenant_id"},
			{Name: "placed_at"}, {Name: "ghost"}},
	}})
	svc, ex, _ := newHelloSluiceServiceWithSchema(t, cache, fakeNamePolicy)
	for _, sql := range []string{
		"SELECT c.name AS ghost, alias.* FROM shop.main.customers c, shop.main.orders alias LIMIT 2",
		"SELECT c.name AS ghost, * FROM shop.main.customers c, shop.main.orders alias LIMIT 2",
		`SELECT c.name AS ghost, "ALIAS".* FROM shop.main.customers c, shop.main.orders "ALIAS" LIMIT 2`,
		"SELECT c.name AS ghost, alias.* FROM shop.main.customers c JOIN shop.main.orders alias ON true LIMIT 2",
	} {
		before := ex.calls
		rows, err := runRows(svc, sql)
		if err == nil {
			t.Errorf("%s: served %v, want ERR_MASK_UNSUPPORTED_CONTEXT", sql, rows)
			continue
		}
		if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeMaskContext {
			t.Errorf("%s: code = %s (%s), want ERR_MASK_UNSUPPORTED_CONTEXT", sql, ae.Code, ae.Message)
		}
		if ex.calls != before {
			t.Errorf("%s: executor was reached", sql)
		}
	}
}

// stubLoader fails every catalog load with err. A nil err loads a catalog
// without tables.
type stubLoader struct{ err error }

func (l stubLoader) Load(context.Context, string) (pkgds.Schema, error) {
	return pkgds.Schema{}, l.err
}

// A masked table needs its columns from the schema cache. Only a table
// that a loaded catalog does not have is a rewrite failure. A load that
// fails keeps its own code: the client can retry it.
func TestExecute_SchemaLoadErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want pkgerr.Code
	}{
		{"canceled during the load", fmt.Errorf("schema: borrow conn: %w", context.Canceled), pkgerr.CodeCanceled},
		{"deadline during the load", fmt.Errorf("schema: introspect: %w", context.DeadlineExceeded), pkgerr.CodeTimeout},
		{"load fails", errors.New("introspection failed"), pkgerr.CodeDataSourceUnavailable},
		{"catalog not attached", schema.ErrUnknownCatalog, pkgerr.CodeDataSourceUnavailable},
		{"table not in the loaded catalog", nil, pkgerr.CodeRewriteFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := schema.New(schema.Options{Loader: stubLoader{err: tc.err}})
			svc, ex, a := newHelloSluiceServiceWithSchema(t, cache)
			rows, err := runRows(svc, "SELECT id FROM shop.main.customers")
			if err == nil {
				t.Fatalf("served %v, want %s", rows, tc.want)
			}
			if ae := pkgerr.FromError(err); ae.Code != tc.want {
				t.Fatalf("code = %s (%s), want %s", ae.Code, ae.Message, tc.want)
			}
			if ex.calls != 0 {
				t.Fatal("executor was reached")
			}
			recs := a.all()
			if len(recs) != 1 || recs[0].ErrorCode != string(tc.want) {
				t.Fatalf("audit records = %+v, want one with %s", recs, tc.want)
			}
		})
	}
}

// On a query with a post-query mask a star over orders, which has none, is
// expanded from the schema cache too. A load that fails there keeps its
// code. It is not a star the caller has to list.
func TestExecute_SchemaLoadErrorCodesUnderStar(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want pkgerr.Code
	}{
		{"canceled during the load", fmt.Errorf("schema: borrow conn: %w", context.Canceled), pkgerr.CodeCanceled},
		{"deadline during the load", fmt.Errorf("schema: introspect: %w", context.DeadlineExceeded), pkgerr.CodeTimeout},
		{"load fails", errors.New("introspection failed"), pkgerr.CodeDataSourceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := schema.New(schema.Options{Loader: stubLoader{err: tc.err}})
			svc, ex, _ := newHelloSluiceServiceWithSchema(t, cache, fakeNamePolicy)
			for _, sql := range []string{
				"SELECT * FROM shop.main.orders WHERE customer_id IN (SELECT id FROM shop.main.customers) ORDER BY id",
				"SELECT o.* FROM shop.main.orders o JOIN shop.main.customers c ON c.id = o.customer_id",
			} {
				rows, err := runRows(svc, sql)
				if err == nil {
					t.Errorf("%s: served %v, want %s", sql, rows, tc.want)
					continue
				}
				if ae := pkgerr.FromError(err); ae.Code != tc.want {
					t.Errorf("%s: code = %s (%s), want %s", sql, ae.Code, ae.Message, tc.want)
				}
			}
			if ex.calls != 0 {
				t.Error("executor was reached")
			}
		})
	}
}

// The gateway passes these forms on and DuckDB 1.5.5 refuses them. Each one
// reads the table wrapper (golden fixture 104, the rawfails lines of the
// differential corpus). When a DuckDB release serves one, this test fails:
// pin the masked rows then.
func TestExecute_FormsDuckDBRefusesReadTheWrapper(t *testing.T) {
	svc, ex, _ := newHelloSluiceService(t)
	for _, sql := range []string{
		"SELECT to_json(c.*) AS j FROM shop.main.customers c",
		"SELECT row(c.*) AS r FROM shop.main.customers c",
		"SELECT struct_pack(c.*) AS r FROM shop.main.customers c",
		"SELECT x.email FROM shop.main.customers c, LATERAL (SELECT c.*) x",
		"SELECT x.email FROM shop.main.customers c, LATERAL (SELECT *) x",
		"SELECT shop.main.customers.email FROM shop.main.customers c",
	} {
		before := ex.calls
		rows, err := runRows(svc, sql)
		if err == nil {
			t.Errorf("%s: served %v, DuckDB refused this form before", sql, rows)
		} else if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeQueryFailed {
			t.Errorf("%s: code = %s, want ERR_QUERY_FAILED", sql, ae.Code)
		}
		if ex.calls != before+1 {
			t.Errorf("%s: executor ran %d times, want 1", sql, ex.calls-before)
		}
	}
}

// DuckDB binds s.ssn to the FROM item s and fails. It does not fall back to
// the select alias s of the post-query masked item. If a release does, the
// statement returns the raw field and this test fails.
func TestExecute_SelectAliasIsNoQualifier(t *testing.T) {
	svc, _, _ := newSeededHelloSluiceService(t, nil, peopleSeed, fakeNamePolicy, peoplePolicies)
	for _, sql := range []string{
		"SELECT p.id, p.info AS s, s.ssn AS leak FROM shop.main.people p, (SELECT 1 AS one) s ORDER BY p.id",
		"SELECT p.id, p.info AS s, s.ssn AS leak FROM shop.main.people p, shop.main.orders s ORDER BY p.id",
		"SELECT p.id, p.info AS s FROM shop.main.people p, (SELECT 1 AS one) s ORDER BY s.ssn DESC",
	} {
		if rows, err := runRows(svc, sql); err == nil {
			t.Errorf("%s: served %v, want a DuckDB binder error", sql, rows)
		}
	}
	// A FROM item named like the select alias of the masked item is served.
	for sql, n := range map[string]int{
		"SELECT c.name AS o, o.id FROM shop.main.customers c JOIN shop.main.orders o ON o.customer_id = c.id":                      3,
		"SELECT c.name AS o FROM shop.main.customers c WHERE EXISTS (SELECT 1 FROM shop.main.orders o WHERE o.customer_id = c.id)": 2,
		"SELECT name AS customers, * FROM shop.main.customers":                                                                     2,
	} {
		rows, err := runRows(svc, sql)
		if got := fmt.Sprint(rows); err != nil || len(rows) != n || strings.Contains(got, "Alice") || strings.Contains(got, "Bob") {
			t.Errorf("%s: rows = %s, err = %v, want %d rows with fake names", sql, got, err, n)
		}
	}
}

const allowWithoutTables = `
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: allow-without-tables
  priority: 100
spec:
  effect: allow
  match:
    any:
      - subjects: { groups: ["analytics"] }
`

// pg_query cannot parse these DuckDB statements, so they end before the
// executor. SUMMARIZE would return the min and max of the raw column. The
// table walker finds no table in some of them, so the caller gets an allow
// that names no table.
func TestExecute_DuckDBOnlyStatementsNotExecuted(t *testing.T) {
	svc, ex, _ := newHelloSluiceService(t, allowWithoutTables)
	for _, sql := range []string{
		"SUMMARIZE shop.main.customers",
		"SUMMARIZE SELECT email FROM shop.main.customers",
		"DESCRIBE shop.main.customers",
		"PIVOT shop.main.customers ON tenant_id USING max(email)",
		"FROM shop.main.customers SELECT email",
		"SELECT * EXCLUDE (id) FROM shop.main.customers",
		"SELECT * REPLACE (upper(name) AS name) FROM shop.main.customers",
		"SELECT [c] AS l FROM shop.main.customers c",
		"SELECT email FROM shop.main.customers GROUP BY ALL",
	} {
		before := ex.calls
		rows, err := runRows(svc, sql)
		if err == nil {
			t.Errorf("%s: served %v, want ERR_SYNTAX", sql, rows)
		} else if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeSyntax {
			t.Errorf("%s: code = %s (%s), want ERR_SYNTAX", sql, ae.Code, ae.Message)
		}
		if ex.calls != before {
			t.Errorf("%s: executor was reached", sql)
		}
	}
}

// SET and SHOW name no table, so only an allow policy that names no table
// matches them. A setting DuckDB accepts would stay on the pooled
// connection for later requests of any subject, and SHOW lists tables a
// deny policy covers. Neither reaches the executor.
func TestExecute_SetAndShowNotExecuted(t *testing.T) {
	svc, ex, _ := newHelloSluiceService(t, allowWithoutTables)
	for _, sql := range []string{
		"SET threads = 1",
		"SET search_path = 'shop.main'",
		"RESET threads",
		"SHOW TABLES",
		"SHOW ALL",
		"SHOW search_path",
	} {
		before := ex.calls
		rows, err := runRows(svc, sql)
		if err == nil {
			t.Errorf("%s: served %v, want ERR_UNSUPPORTED_SYNTAX", sql, rows)
		} else if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeUnsupportedSyntax {
			t.Errorf("%s: code = %s (%s), want ERR_UNSUPPORTED_SYNTAX", sql, ae.Code, ae.Message)
		}
		if ex.calls != before {
			t.Errorf("%s: executor was reached", sql)
		}
	}
	// The refused search_path is not active: a name without catalog does
	// not resolve to the attached table.
	if rows, err := runRows(svc, "SELECT count(*) FROM customers"); err == nil {
		t.Errorf("table name without catalog served %v after a refused SET search_path", rows)
	}
}

// orders has a row filter and no mask. A post-query mask on a table read
// only in a subquery must not refuse a star over orders.
func TestExecute_StarOnFilteredTableWithPostMaskInSubquery(t *testing.T) {
	svc, _, _ := newHelloSluiceService(t, fakeNamePolicy)
	sql := "SELECT * FROM shop.main.orders WHERE customer_id IN (SELECT id FROM shop.main.customers) ORDER BY id"
	rows, err := runRows(svc, sql)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := "[[101 1 12900 acme 2026-03-01T12:00:00Z] [102 1 4500 acme 2026-03-02T14:20:00Z] [103 2 19900 acme 2026-03-05T10:10:00Z]]"
	if got := fmt.Sprint(rows); got != want {
		t.Fatalf("rows = %s, want %s", got, want)
	}
}

// These forms were refused because a mask applied where a column is
// referenced could not follow them. A SQL mask applied in the table wrapper
// reaches every one, so they are served with the masked values.
func TestExecute_FormerMaskRefusalsServeMaskedValues(t *testing.T) {
	const (
		aliceRow = "map[created_at:2026-01-03T10:00:00Z email:<nil> id:1 name:Alice tenant_id:acme]"
		bobRow   = "map[created_at:2026-01-04T11:30:00Z email:<nil> id:2 name:Bob tenant_id:acme]"
		rowsBoth = "[[" + aliceRow + "] [" + bobRow + "]]"
		twoNulls = "[[<nil>] [<nil>]]"
	)
	cases := []struct {
		name, sql string
		want      string // exact rows, or "" to check only for addresses
		n         int    // the row count when want is ""
	}{
		{"FROM item named like a masked column", "SELECT email.id FROM shop.main.orders email JOIN shop.main.customers c ON email.customer_id = c.id ORDER BY 1", "[[101] [102] [103]]", 0},
		{"output alias named like the table alias in HAVING", "SELECT c.tenant_id, count(*) AS c FROM shop.main.customers c GROUP BY c.tenant_id HAVING c > 0", "[[acme 2]]", 0},
		{"output alias named like the table in HAVING", "SELECT count(*) AS customers FROM shop.main.customers GROUP BY tenant_id HAVING customers > 1", "[[2]]", 0},
		{"output alias named like the table alias in the select list", "SELECT c.tenant_id AS c, to_json(c) AS j FROM shop.main.customers c ORDER BY c.id",
			"[[acme " + aliceRow + "] [acme " + bobRow + "]]", 0},
		{"whole row", "SELECT c FROM shop.main.customers c ORDER BY c.id", rowsBoth, 0},
		{"whole row, table name", "SELECT customers FROM shop.main.customers ORDER BY customers.id", rowsBoth, 0},
		{"whole row without a sort key", "SELECT c FROM shop.main.customers c", "", 2},
		{"whole row expanded by unnest", "SELECT unnest(c) FROM shop.main.customers c", "", 2},
		{"whole row field by name", "SELECT struct_extract(c, 'email') AS e FROM shop.main.customers c ORDER BY c.id", twoNulls, 0},
		{"whole row in a lambda", "SELECT list_transform(ARRAY[c], x -> x.email) AS e FROM shop.main.customers c ORDER BY c.id", "[[[<nil>]] [[<nil>]]]", 0},
		{"whole row in an aggregate", "SELECT max(c) FROM shop.main.customers c", "[[" + bobRow + "]]", 0},
		{"table alias named like the masked column", "SELECT email FROM shop.main.customers email", twoNulls, 0},
		{"star in a function that drops it", "SELECT count(c.*) AS n FROM shop.main.customers c", "[[2]]", 0},
		{"whole row as function argument", "SELECT to_json(c) AS j FROM shop.main.customers c ORDER BY c.id", rowsBoth, 0},
		{"whole row field access", "SELECT (c).email AS e FROM shop.main.customers c ORDER BY c.id", twoNulls, 0},
		{"whole row subscript", "SELECT c['email'] AS e FROM shop.main.customers c ORDER BY c.id", twoNulls, 0},
		{"whole row in a predicate", "SELECT id FROM shop.main.customers c WHERE CAST(c AS VARCHAR) LIKE '%alice@%'", "[]", 0},
		{"whole row inside a subquery", "SELECT x FROM (SELECT c AS x FROM shop.main.customers c) q", "", 2},
		{"whole row from an outer scope", "SELECT (SELECT to_json(c)) AS j FROM shop.main.customers c", "", 2},
		{"whole row, quoted alias in upper case", `SELECT "C" FROM shop.main.customers c`, "", 2},
		{"whole row, alias named like a column of an outer table", "SELECT (SELECT placed_at FROM shop.main.customers placed_at ORDER BY id LIMIT 1) AS x FROM shop.main.orders o LIMIT 1", "", 1},
		{"positional column reference", "SELECT #3 FROM shop.main.customers", twoNulls, 0},
		{"column alias list on the table", "SELECT c.e2 FROM shop.main.customers AS c(i, n, e2, t, cr) ORDER BY c.i", twoNulls, 0},
		{"join alias", "SELECT j.email FROM (shop.main.customers c JOIN shop.main.orders o ON o.customer_id = c.id) AS j", "[[<nil>] [<nil>] [<nil>]]", 0},
		{"join alias with column list", "SELECT j.c3 FROM (shop.main.customers c JOIN shop.main.orders o ON o.customer_id = c.id) AS j(c1, c2, c3)", "[[<nil>] [<nil>] [<nil>]]", 0},
		{"star in a pattern match", "SELECT c.* LIKE '%mail' FROM shop.main.customers c", twoNulls, 0},
		{"star as a sort key", "SELECT id FROM shop.main.customers c ORDER BY c.*", "[[1] [2]]", 0},
		{"inner alias hides the outer masked table", "SELECT (SELECT c.email FROM shop.main.orders c LIMIT 1) AS e FROM shop.main.customers c", twoNulls, 0},
		{"CTE alias hides the outer masked table", "WITH k AS (SELECT 1 AS one) SELECT (SELECT c.email FROM k c LIMIT 1) AS e FROM shop.main.customers c", twoNulls, 0},
		{"table and CTE under one alias", "WITH t AS (SELECT 1 AS x) SELECT c.email FROM shop.main.customers c, t c LIMIT 2", twoNulls, 0},
		{"two FROM items under one alias", "SELECT c.email FROM shop.main.customers c JOIN shop.main.orders c ON total_cents > 0 AND name IS NOT NULL LIMIT 2", twoNulls, 0},
		{"star over FULL JOIN USING", "SELECT * FROM shop.main.orders o FULL JOIN shop.main.customers c USING (id) ORDER BY 1",
			"[[1 <nil> <nil> <nil> <nil> Alice <nil> acme 2026-01-03T10:00:00Z] [2 <nil> <nil> <nil> <nil> Bob <nil> acme 2026-01-04T11:30:00Z] " +
				"[101 1 12900 acme 2026-03-01T12:00:00Z <nil> <nil> <nil> <nil>] [102 1 4500 acme 2026-03-02T14:20:00Z <nil> <nil> <nil> <nil>] " +
				"[103 2 19900 acme 2026-03-05T10:10:00Z <nil> <nil> <nil> <nil>]]", 0},
		{"star over RIGHT JOIN USING in a subquery", "SELECT q.id FROM (SELECT * FROM shop.main.customers c RIGHT JOIN shop.main.orders o USING (id)) q ORDER BY 1", "[[101] [102] [103]]", 0},
		{"star over NATURAL RIGHT JOIN", "SELECT * FROM shop.main.customers c NATURAL RIGHT JOIN shop.main.orders o ORDER BY 1",
			"[[101 <nil> <nil> acme <nil> 1 12900 2026-03-01T12:00:00Z] [102 <nil> <nil> acme <nil> 1 4500 2026-03-02T14:20:00Z] [103 <nil> <nil> acme <nil> 2 19900 2026-03-05T10:10:00Z]]", 0},
	}
	svc, _, _ := newHelloSluiceService(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := runRows(svc, tc.sql)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			got := fmt.Sprint(rows)
			switch {
			case strings.Contains(got, "@"):
				t.Fatalf("unmasked email in result: %s", got)
			case tc.want != "" && got != tc.want:
				t.Fatalf("rows = %s, want %s", got, tc.want)
			case tc.want == "" && len(rows) != tc.n:
				t.Fatalf("got %d rows, want %d: %s", len(rows), tc.n, got)
			}
		})
	}

	// admin_users has a mask and no row filter.
	svc, _, _ = newHelloSluiceService(t, adminUsersPolicies)
	for sql, want := range map[string]string{
		"SELECT admin_users FROM shop.main.admin_users ORDER BY admin_users.id": "[[map[id:1 role:superuser username:<nil>]] [map[id:2 role:maintainer username:<nil>]]]",
		"SELECT #2 FROM shop.main.admin_users":                                  twoNulls,
	} {
		rows, err := runRows(svc, sql)
		if err != nil {
			t.Errorf("%s: %v", sql, err)
			continue
		}
		if got := fmt.Sprint(rows); got != want {
			t.Errorf("%s: rows = %s, want %s", sql, got, want)
		}
	}
}

const approvalOnAdminUsers = `
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: allow-shop
  priority: 100
spec:
  effect: allow
  match:
    any:
      - subjects: { groups: ["analytics"] }
        resources: { catalogs: ["shop"], schemas: ["main"], tables: ["*"] }
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ApprovalPolicy
metadata:
  name: approve-admin-reads
  priority: 100
spec:
  match:
    any:
      - resources: { catalogs: ["shop"], schemas: ["main"], tables: ["admin_users"] }
  when:
    columnsAccessed: ["username"]
    predicates:
      - { column: "role", op: "=", value: "superuser" }
`

// An ApprovalPolicy with columnsAccessed must hold every statement that
// DuckDB would answer with the column, also when the statement does not
// name it. The service has no broker here, so a held query fails before
// the executor, and a query that is not held is served by DuckDB.
func TestExecute_ApprovalTriggersAgainstDuckDB(t *testing.T) {
	svc, ex, _ := newHelloSluiceService(t, approvalOnAdminUsers)
	held := []string{
		// columnsAccessed: the column by name, in any case.
		"SELECT username FROM shop.main.admin_users",
		`SELECT "USERNAME" FROM shop.main.admin_users`,
		`SELECT a."UserName" FROM shop.main.admin_users a`,
		"SELECT greatest(username, '') FROM shop.main.admin_users",
		// columnsAccessed: the column without its name.
		"SELECT * FROM shop.main.admin_users",
		"SELECT a FROM shop.main.admin_users a",
		`SELECT "A" FROM shop.main.admin_users a`,
		`SELECT a FROM shop.main.admin_users "A"`,
		`SELECT "ADMIN_USERS" FROM shop.main.admin_users`,
		"SELECT (a).username FROM shop.main.admin_users a",
		"SELECT #2 FROM shop.main.admin_users",
		"SELECT x FROM shop.main.admin_users a(i, x, r)",
		"SELECT j FROM (shop.main.admin_users a JOIN shop.main.orders o ON o.id = a.id) AS j",
		"SELECT j.c2 FROM (shop.main.admin_users a JOIN shop.main.orders o ON o.id = a.id) AS j(c1, c2)",
		"SELECT id FROM shop.main.admin_users NATURAL JOIN (SELECT 'root' AS username) u",
		// predicates: the value, and a condition that hides the value.
		"SELECT id FROM shop.main.admin_users WHERE role = 'superuser'",
		`SELECT id FROM shop.main.admin_users WHERE "ROLE" = 'superuser'`,
		"SELECT id FROM shop.main.admin_users WHERE role IN ('analyst', 'superuser')",
		"SELECT id FROM shop.main.admin_users WHERE lower(role) = 'superuser'",
		"SELECT id FROM shop.main.admin_users WHERE role NOT IN ('analyst')",
		"SELECT id FROM shop.main.admin_users WHERE NOT (role <> 'superuser')",
		"SELECT id FROM shop.main.admin_users WHERE role = 'superuser'::VARCHAR",
	}
	for _, sql := range held {
		before := ex.calls
		rows, err := runRows(svc, sql)
		if err == nil {
			t.Errorf("%s: served %v, want held for approval", sql, rows)
		} else if !strings.Contains(err.Error(), "approval") {
			t.Errorf("%s: err = %v, want held for approval", sql, err)
		}
		if ex.calls != before {
			t.Errorf("%s: executor was reached", sql)
		}
	}
	served := []string{
		"SELECT id FROM shop.main.admin_users",
		"SELECT id, role FROM shop.main.admin_users WHERE role = 'analyst'",
		"SELECT id FROM shop.main.admin_users WHERE role IN ('analyst', 'auditor')",
		// The wrapper BI tools put around a query.
		"SELECT * FROM (SELECT id, role FROM shop.main.admin_users) AS virtual_table LIMIT 100",
		"WITH t AS (SELECT id FROM shop.main.admin_users) SELECT * FROM t",
		"SELECT a.id FROM shop.main.admin_users a WHERE a.id = 1",
	}
	for _, sql := range served {
		if _, err := runRows(svc, sql); err != nil {
			t.Errorf("%s: %v, want served without approval", sql, err)
		}
	}
}
