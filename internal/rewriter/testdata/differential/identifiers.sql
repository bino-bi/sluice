# Identifiers. DuckDB resolves names without regard to case, also quoted
# ones, so the case of a column, table or function name does not matter. The
# case of an alias does: it becomes a result column name, a struct key or a
# to_json key. The normalizer gives pg_query's lower-case alias names the
# caller's spelling back.

# Case of names.
same SELECT ID, A FROM T ORDER BY ID
same SELECT "id", "A" FROM "t" ORDER BY 1
same SELECT t.id, T.a FROM T ORDER BY t.id
same SELECT x.id FROM t AS X ORDER BY X.id
same SELECT "X".id FROM t AS x ORDER BY 1
same SELECT Upper(s), LENGTH(s), Coalesce(a, 0) FROM t ORDER BY 1, 2, 3
same SELECT id FROM t WHERE "S" = 'Alice' ORDER BY ID
same SELECT "T".ID FROM main.T WHERE T.Tenant = 'k' ORDER BY 1
same SELECT count(*) FROM memory.main.t
same SELECT count(*) FROM "memory"."main"."t"
same SELECT T.id FROM Memory.Main.T ORDER BY 1
same WITH "Totals" AS (SELECT a, count(*) AS n FROM t GROUP BY a) SELECT a, n FROM totals ORDER BY a
same WITH c AS (SELECT ID AS Key FROM t) SELECT key FROM C ORDER BY KEY

# Alias spelling comes back as written.
same SELECT id AS MyId FROM t ORDER BY MyId
same SELECT id AS "MyId" FROM t ORDER BY "MyId"
same SELECT count(*) AS Total, sum(a) AS SumA FROM t
same SELECT x.MyId FROM (SELECT id AS MyId FROM t) x ORDER BY 1
same SELECT count(*) FROM (SELECT id AS MyId FROM t) x WHERE to_json(x)::text LIKE '%MyId%'
same SELECT to_json(x) FROM (SELECT id AS MyId, s AS Name FROM t) x ORDER BY x.MyId
same SELECT struct_pack(MyKey := id) FROM t ORDER BY id
same SELECT struct_pack(MyKey := id, Other := s) FROM t ORDER BY id
same SELECT id FROM t WHERE struct_pack(MyKey := id)::text LIKE '%MyKey%' ORDER BY id
same WITH c(MyCol) AS (SELECT id FROM t) SELECT to_json(c) FROM c ORDER BY MyCol
same WITH c(MyCol, Other) AS (SELECT id, s FROM t) SELECT c.MyCol, to_json(c) FROM c ORDER BY 1
same SELECT v2.MyCol FROM (VALUES (1), (2)) AS v2(MyCol), t WHERE t.id = v2.MyCol ORDER BY t.id
same SELECT to_json(v2) FROM (VALUES (1, 'a')) AS v2(MyCol, Txt), t WHERE t.id = 1
same SELECT row_to_json(x) FROM (SELECT a AS SomeA FROM t WHERE id = 1) x
same SELECT id, to_json(struct_pack(InCase := a)) FROM t ORDER BY id
same SELECT x FROM (SELECT struct_pack(Deep := struct_pack(Inner := a)) AS x FROM t WHERE id = 1) q
same SELECT (SELECT to_json(y) FROM (SELECT t.a AS Corr) y) FROM t ORDER BY t.id
same SELECT unnest(ARRAY[struct_pack(Kk := id)]) FROM t ORDER BY 1
same SELECT name AS Value, id AS Key FROM (SELECT s AS name, id FROM t) q ORDER BY Key
same SELECT id AS Name, s AS Value FROM t ORDER BY Name
same SELECT a AS "UPPER", b AS lower FROM t ORDER BY id

# Column refs keep the catalog name, and a name spelled two ways keeps
# pg_query's lower case. Neither changes rows.
same SELECT id AS MyId FROM t WHERE myid > 0 ORDER BY id
same SELECT MyId FROM (SELECT id AS myid FROM t) q ORDER BY 1
same SELECT ID FROM t ORDER BY 1
same SELECT q.MYID FROM (SELECT id AS MyId FROM t) q ORDER BY 1

# Quoted names that are keywords in DuckDB only. pg_query deparses them
# bare, and DuckDB cannot parse that.
ERR_UNSUPPORTED_SYNTAX WITH "at" AS (SELECT id FROM t) SELECT id FROM "at" ORDER BY id
ERR_UNSUPPORTED_SYNTAX WITH "by" AS (SELECT id FROM t) SELECT id FROM "by" ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT x."qualify" FROM (SELECT id AS "qualify" FROM t) x("qualify") ORDER BY 1
ERR_UNSUPPORTED_SYNTAX SELECT q."pivot" FROM t, (VALUES (1)) AS q("pivot") WHERE t.id = 1
ERR_UNSUPPORTED_SYNTAX SELECT sum(id) OVER "by" FROM t WINDOW "by" AS (ORDER BY id)
ERR_UNSUPPORTED_SYNTAX SELECT x."show" FROM (SELECT id FROM t) AS x("show") ORDER BY 1
ERR_UNSUPPORTED_SYNTAX SELECT q.id FROM (SELECT id, a AS "pivot" FROM t) q WHERE "pivot" > 0 ORDER BY 1
ERR_UNSUPPORTED_SYNTAX WITH c AS (SELECT q.id FROM (SELECT id, a AS "unpivot" FROM t) q WHERE "unpivot" IS NULL) SELECT id FROM c

# pg_query deparses named-arg labels and window names bare, also PostgreSQL
# keywords.
ERR_UNSUPPORTED_SYNTAX SELECT struct_pack("default" := id) FROM t ORDER BY id

# Keyword names that need no quotes after AS or a dot, or that DuckDB
# accepts bare in that spot.
same SELECT sum(id) OVER "current_date" FROM t WINDOW "current_date" AS (ORDER BY id)
same SELECT id AS "by", a AS "at" FROM t ORDER BY 1
same SELECT 1 AS "select" FROM t WHERE id = 1
same SELECT x.id FROM (SELECT id FROM t) AS x ORDER BY x.id
same SELECT id AS "order", a AS "group" FROM t ORDER BY "order"
same SELECT st.x AS "x", st.y AS "Y" FROM t ORDER BY id

# A DuckDB keyword as a table alias is refused at parse time.
ERR_SYNTAX SELECT id FROM t AS "lambda" ORDER BY id
ERR_SYNTAX SELECT x.id FROM (SELECT id FROM t) x(qualify) ORDER BY 1
ERR_SYNTAX SELECT semi.id FROM t semi ORDER BY 1
ERR_SYNTAX SELECT x.id FROM t AS asof JOIN u x ON true
ERR_SYNTAX SELECT id FROM t positional ORDER BY id

# Identifiers longer than 63 bytes: pg_query would cut them.
ERR_UNSUPPORTED_SYNTAX SELECT payroll_salaries_by_employee_and_month_from_hr_system_snapshot_2026.id FROM t AS payroll_salaries_by_employee_and_month_from_hr_system_snapshot_2026
ERR_UNSUPPORTED_SYNTAX SELECT id AS payroll_salaries_by_employee_and_month_from_hr_system_snapshot_2026 FROM t
ERR_UNSUPPORTED_SYNTAX WITH payroll_salaries_by_employee_and_month_from_hr_system_snapshot_2026x AS (SELECT 1 AS id) SELECT p.id FROM t p
ERR_UNSUPPORTED_SYNTAX SELECT id FROM t WHERE "payroll_salaries_by_employee_and_month_from_hr_system_snapshot_2026" IS NULL
same SELECT id AS payroll_salaries_by_employee_and_month_from_hr_system_snapshot_ FROM t ORDER BY 1

# A column qualified with the schema or catalog of a filtered table that
# has no alias: the wrapper is named after the table alone.
ERR_UNSUPPORTED_SYNTAX SELECT main.t.id FROM main.t ORDER BY 1
ERR_UNSUPPORTED_SYNTAX SELECT main.t.id FROM t ORDER BY 1
ERR_UNSUPPORTED_SYNTAX SELECT memory.main.t.id FROM t ORDER BY 1
ERR_UNSUPPORTED_SYNTAX SELECT memory.t.id FROM t ORDER BY 1
ERR_UNSUPPORTED_SYNTAX SELECT "memory"."main"."t"."a" FROM "memory"."main"."t" ORDER BY 1
ERR_UNSUPPORTED_SYNTAX SELECT main.t.st.x FROM t ORDER BY 1
ERR_UNSUPPORTED_SYNTAX SELECT id FROM t WHERE main.t.a = 1 ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT (SELECT max(main.u.a) FROM u WHERE u.id = t.id) FROM t ORDER BY t.id
same SELECT t.id FROM main.t ORDER BY 1
same SELECT x.id FROM main.t x ORDER BY 1
same SELECT t.st.x FROM t ORDER BY t.id
same SELECT q.t.x FROM (SELECT id, st AS t FROM u) q JOIN t ON t.id = q.id ORDER BY q.id
rawfails SELECT main.t.id FROM main.t x

# Unicode and odd names.
same SELECT id AS "日本", a AS "ünï" FROM t ORDER BY 1
same SELECT id AS "a b", a AS "x""y" FROM t ORDER BY 1
same SELECT "a b" FROM (SELECT id AS "a b" FROM t) q ORDER BY 1
ERR_SYNTAX SELECT U&"d\0061t" FROM t
