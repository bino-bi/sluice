# Mask bypass attempts. t.secret has the constant mask '***', u.secret a
# post-query mask, v.secret_n the constant mask 2. SQL masks are applied in
# the table wrapper. kw has a post-query mask on a column called
# current_date.

# t.secret, constant mask: every use reads the mask output.
same SELECT id, secret FROM t ORDER BY id
same SELECT secret FROM t
same SELECT t.secret, T.SECRET, "secret" FROM t ORDER BY t.id
same SELECT secret, count(*) FROM t GROUP BY secret
same SELECT id, upper(secret), length(secret), secret || s FROM t ORDER BY id
same SELECT id FROM t WHERE secret = 's1' ORDER BY id
same SELECT id FROM t WHERE secret IS NULL ORDER BY id
same SELECT id FROM t WHERE secret LIKE 's%' ORDER BY id
same SELECT id FROM t WHERE secret IN ('s1', 's2', '***') ORDER BY id
same SELECT id, secret AS x, x AS y, upper(x) FROM t ORDER BY id
same SELECT id, secret AS x, alias.x AS y FROM t ORDER BY id
same SELECT id, secret AS x FROM t WHERE x = 's1' ORDER BY id
same SELECT id, secret AS x FROM t ORDER BY x, id
same SELECT secret AS x, count(*) FROM t GROUP BY x
same SELECT id, secret AS x, (SELECT x) FROM t ORDER BY id
same SELECT id, (SELECT secret) FROM t ORDER BY id
same SELECT id, (SELECT t.secret FROM u WHERE u.id = t.id) FROM t ORDER BY id
same SELECT id, (SELECT max(t.secret) FROM t) FROM u ORDER BY id
same SELECT DISTINCT secret FROM t
same SELECT id, secret FROM t ORDER BY 2, id
same SELECT secret, count(*) FROM t GROUP BY 1
same SELECT * FROM t ORDER BY id
same SELECT t.* FROM t ORDER BY id
same SELECT x.secret FROM (SELECT * FROM t) x ORDER BY x.id
same SELECT x.e FROM (SELECT secret AS e, id FROM t) x ORDER BY x.id
same SELECT x.c3 FROM (SELECT id, s, secret FROM t) AS x(c1, c2, c3) ORDER BY x.c1
same WITH c AS (SELECT secret FROM t) SELECT secret FROM c
same WITH c AS (SELECT * FROM t) SELECT secret, id FROM c ORDER BY id
same SELECT secret FROM t UNION ALL SELECT secret FROM t
same SELECT secret FROM t UNION SELECT s FROM t
same SELECT max(secret) FILTER (WHERE id > 2), min(secret) FROM t
same SELECT string_agg(secret, ',' ORDER BY id) FROM t
same SELECT list(secret ORDER BY id) FROM t
same SELECT id, first_value(secret) OVER (ORDER BY id) FROM t ORDER BY id
same SELECT id, count(*) OVER (PARTITION BY secret) FROM t ORDER BY id
same SELECT id, row_number() OVER (ORDER BY secret, id) FROM t ORDER BY id
same SELECT id, lag(secret) OVER w FROM t WINDOW w AS (ORDER BY id) ORDER BY id
same SELECT t.id, u.id FROM t JOIN u ON u.s = upper(t.secret) ORDER BY t.id, u.id
same SELECT t.id FROM t JOIN u ON t.secret = u.s ORDER BY t.id
same SELECT id, CASE WHEN secret = 's1' THEN 'leak' ELSE 'ok' END FROM t ORDER BY id
same SELECT id, coalesce(secret, 'none'), nullif(secret, 's1') FROM t ORDER BY id
same SELECT id, struct_pack(k := secret), ARRAY[secret] FROM t ORDER BY id
same SELECT id, secret COLLATE NOCASE FROM t ORDER BY id
same SELECT id, secret::VARCHAR, CAST(secret AS BLOB) FROM t ORDER BY id
same SELECT count(*) FROM t WHERE EXISTS (SELECT 1 FROM u WHERE u.s = t.secret)
same SELECT id FROM t WHERE id IN (SELECT id FROM t WHERE secret = 's2') ORDER BY id
same SELECT x.id FROM t, LATERAL (SELECT t.id, t.secret) x WHERE x.secret = 's1' ORDER BY x.id
same SELECT q.secret FROM (SELECT secret FROM (SELECT * FROM t) y) q
same SELECT v2.e FROM t, LATERAL (VALUES (t.secret)) v2(e) ORDER BY t.id
same SELECT id, (VALUES (secret)) FROM t ORDER BY id
same SELECT id, md5(secret), hash(secret) FROM t ORDER BY id
same SELECT id, secret FROM t ORDER BY id LIMIT 2
same SELECT id, secret FROM t ORDER BY id LIMIT 2 OFFSET 3
# The whole row is the row of the wrapper.
same SELECT t FROM t ORDER BY t.id
same SELECT unnest(t) FROM t
same SELECT to_json(t) FROM t ORDER BY t.id
same SELECT struct_extract(t, 'secret'), (t).secret, t['secret'] FROM t ORDER BY t.id
same SELECT list_transform(ARRAY[t], x -> x.secret) FROM t ORDER BY t.id
same SELECT max(t) FROM t
same SELECT secret FROM t secret
same SELECT secret.id, secret.secret FROM t secret ORDER BY 1

# u.secret, post-query mask: a bare top-level item is masked after the run.
same SELECT id, secret FROM u ORDER BY id
same SELECT secret FROM u
same SELECT id, secret AS s2 FROM u ORDER BY id
same SELECT u.id, u.secret FROM u ORDER BY u.id
same SELECT * FROM u ORDER BY id
same SELECT secret, id FROM u WHERE id > 2 ORDER BY id
same SELECT t.id, u.secret FROM t JOIN u ON u.id = t.id ORDER BY t.id
same SELECT id, secret FROM u ORDER BY id LIMIT 3

# Every other use of u.secret is refused.
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, upper(secret) FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id FROM u WHERE secret = 'u1'
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, secret AS x, x AS leak FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, secret AS x, upper(x) FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, secret AS x, (SELECT x) FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, secret AS x FROM u WHERE x = 'u1'
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, secret AS x FROM u ORDER BY x
ERR_MASK_UNSUPPORTED_CONTEXT SELECT secret AS x, count(*) FROM u GROUP BY x
ERR_MASK_UNSUPPORTED_CONTEXT SELECT secret AS x, count(*) FROM u GROUP BY 1 HAVING x > 'u1'
ERR_MASK_UNSUPPORTED_CONTEXT SELECT secret AS x, count(*) OVER (PARTITION BY x) FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, secret AS x FROM u WHERE id IN (SELECT id FROM t WHERE x = 'u1')
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, secret FROM u ORDER BY 2
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, secret FROM u ORDER BY 2 DESC, id LIMIT 1
ERR_MASK_UNSUPPORTED_CONTEXT SELECT secret, count(*) FROM u GROUP BY 1
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, secret, count(*) FROM u GROUP BY ROLLUP (1, 2)
ERR_MASK_UNSUPPORTED_CONTEXT SELECT DISTINCT ON (2) id, secret FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT DISTINCT secret FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT secret FROM u UNION ALL SELECT secret FROM u
# A set operation over a table with a post-query mask is refused, even
# without the masked column.
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id FROM t UNION SELECT id FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT x.secret FROM (SELECT secret FROM u) x
ERR_MASK_UNSUPPORTED_CONTEXT SELECT (SELECT secret FROM u WHERE u.id = t.id) FROM t
ERR_MASK_UNSUPPORTED_CONTEXT WITH c AS (SELECT secret FROM u) SELECT secret FROM c
ERR_MASK_UNSUPPORTED_CONTEXT SELECT t.id FROM t JOIN u USING (secret)
ERR_MASK_UNSUPPORTED_CONTEXT SELECT t.id FROM t NATURAL JOIN u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT t.id FROM t JOIN u ON u.secret = t.s
ERR_MASK_UNSUPPORTED_CONTEXT SELECT count(*) FROM u WHERE secret IS NOT NULL
ERR_MASK_UNSUPPORTED_CONTEXT SELECT max(secret) FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, lag(secret) OVER (ORDER BY id) FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, secret FROM u ORDER BY secret
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, CASE WHEN secret = 'u1' THEN 1 END FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT unnest(struct_pack(p := 1, q := 2)), secret FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, secret::VARCHAR FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT u FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT to_json(u) FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT unnest(u) FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT struct_extract(u, 'secret') FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT max(u) FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT list_transform(ARRAY[u], x -> x.secret) FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT to_json(u.*) FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT row(u.*) FROM u
# A table alias named like the masked column: the bare name is the column.
same SELECT secret FROM u secret
ERR_MASK_UNSUPPORTED_CONTEXT SELECT secret.id FROM u secret
# In a nested SELECT the unqualified name is refused, also when an inner
# table has the column. DuckDB binds it to u where t is not visible yet.
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, (SELECT max(secret) FROM t) FROM u ORDER BY id
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, (SELECT count(*) FROM v JOIN v v2 ON v2.id = v.id AND secret = 'u1' JOIN t ON true) FROM u ORDER BY id
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, (SELECT count(*) FROM v JOIN v v2 ON v2.id = v.id AND secret = 'u1', t) FROM u ORDER BY id
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id FROM u WHERE EXISTS (SELECT 1 FROM v JOIN v v2 ON v2.id = v.id AND secret = 'u1' JOIN t ON true)
# The alias of u is a column of v, which is joined later.
ERR_MASK_UNSUPPORTED_CONTEXT SELECT secret_n.id, (SELECT count(*) FROM main."semi" s1 JOIN main."by" b1 ON to_json(secret_n)::VARCHAR LIKE '%u1%' JOIN v ON true) FROM u secret_n ORDER BY 1
ERR_MASK_UNSUPPORTED_CONTEXT SELECT secret_n.id, count(*) FROM u secret_n JOIN main."semi" s1 ON CAST(secret_n AS VARCHAR) LIKE '%u1%' JOIN v ON true GROUP BY 1 ORDER BY 1
# DuckDB reads alias.<name> as the select alias <name>, on the raw value.
# On a query with a post-query mask the prefix is refused for every item.
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, secret AS x, alias.x AS leak FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, secret AS x, upper(alias.x) FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, secret AS x, (SELECT alias.x) FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, secret AS x FROM u WHERE alias.x = 'u1'
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, secret AS x FROM u ORDER BY alias.x
ERR_MASK_UNSUPPORTED_CONTEXT SELECT secret AS x, count(*) FROM u GROUP BY alias.x
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, secret AS x, alias.x AS leak FROM u, (SELECT 1 AS one) alias
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id AS x, alias.x + 1 FROM u
# A dotted name reads the FROM item its qualifier names, also when the
# select alias of a masked item has the same name.
rawfails SELECT id, secret AS x, x.y AS leak FROM u, (SELECT 1 AS one) x
same SELECT u.id, secret AS x, x.a FROM u, v x WHERE x.id = u.id ORDER BY x.a, u.id
# pg_query reads a bare CURRENT_DATE as a function. DuckDB binds the word
# to a column, a select alias or a table of that name first.
same SELECT id, "current_date" FROM kw ORDER BY id
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, current_date AS d FROM kw ORDER BY id
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, CAST(current_date AS VARCHAR) AS d FROM kw
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, concat(current_date, '') AS d FROM kw
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, lower(CURRENT_DATE) AS d FROM kw
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id FROM kw WHERE CAST(current_date AS VARCHAR) LIKE 'c1%'
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id FROM kw ORDER BY current_date DESC
ERR_MASK_UNSUPPORTED_CONTEXT SELECT count(*) FROM kw GROUP BY current_date
ERR_MASK_UNSUPPORTED_CONTEXT SELECT t.id FROM t JOIN kw ON CAST(current_date AS VARCHAR) = t.s
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, (SELECT max(CAST(current_date AS VARCHAR)) FROM t) FROM kw ORDER BY id
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, secret AS current_date, current_date AS leak FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, secret AS current_date FROM u WHERE CAST(current_date AS VARCHAR) LIKE 'u1%'
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, secret AS current_date FROM u ORDER BY current_date
ERR_MASK_UNSUPPORTED_CONTEXT SELECT current_date FROM u "current_date"
ERR_MASK_UNSUPPORTED_CONTEXT SELECT CAST(current_date AS VARCHAR) FROM u "current_date"
# So is a qualifier that is no FROM item of the query: a schema, a catalog
# or a STRUCT column. A table alias and (col).field are served.
ERR_MASK_UNSUPPORTED_CONTEXT SELECT main.u.id FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT memory.main.u.secret FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, st.x FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT u.id FROM u WHERE EXISTS (SELECT 1 FROM v WHERE main.v.id = u.id)
same SELECT id, (st).x FROM u ORDER BY id
same SELECT u.id, u.st.x, (u.st).y FROM u ORDER BY u.id
same SELECT u.id, x.n, g.i FROM u, (SELECT count(*) AS n FROM v) x, generate_series(1, 2) AS g(i) ORDER BY u.id, g.i
# A column alias list on a table and a join alias are refused on every
# table: star expansion lists a table by its own column names.
ERR_MASK_UNSUPPORTED_CONTEXT SELECT * FROM main."semi" AS s1(a, id) WHERE EXISTS (SELECT 1 FROM u) ORDER BY 1
ERR_MASK_UNSUPPORTED_CONTEXT SELECT u.id FROM u, main."semi" AS s1(x, y) ORDER BY u.id
ERR_MASK_UNSUPPORTED_CONTEXT SELECT j.id FROM (main."semi" s1 JOIN main."by" b1 USING (id)) AS j WHERE EXISTS (SELECT 1 FROM u)

# A star in a nested SELECT over tables without a post-query mask is not
# expanded, so it keeps DuckDB's columns: a USING key once, in its place.
same SELECT id FROM u WHERE id IN (SELECT x.id FROM (SELECT * FROM main."semi" s1) x) ORDER BY id
same SELECT id FROM u WHERE id IN (SELECT id FROM (SELECT * FROM main."semi" s1 JOIN main."by" b1 USING (id) UNION ALL SELECT 5, 6, 'k', 7, 'k') x) ORDER BY id
same SELECT id FROM u WHERE id IN (SELECT id FROM (SELECT * FROM main."semi" s1 JOIN v USING (id) ORDER BY 4 DESC, 1 LIMIT 1) x) ORDER BY id
same SELECT id FROM u WHERE id IN (SELECT id FROM (SELECT * FROM main."semi" s1 FULL JOIN main."by" b1 USING (id)) x) ORDER BY id
same WITH c AS (SELECT * FROM main."semi" s1 NATURAL JOIN main."by" b1) SELECT u.id, u.secret FROM u JOIN c ON c.id = u.id ORDER BY u.id
# The outer select list is expanded, and listing both sides would return
# the USING key twice.
ERR_UNSUPPORTED_SYNTAX SELECT * FROM main."semi" s1 JOIN main."by" b1 USING (id) WHERE s1.id IN (SELECT id FROM u) ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT * FROM u, main."semi" s1 LEFT JOIN main."by" b1 USING (id) ORDER BY u.id
ERR_UNSUPPORTED_SYNTAX SELECT * FROM u, main."semi" s1 NATURAL JOIN main."by" b1 ORDER BY u.id
# A nested star over u itself stays refused.
ERR_MASK_UNSUPPORTED_CONTEXT SELECT x.secret FROM (SELECT * FROM u) x
ERR_MASK_UNSUPPORTED_CONTEXT WITH c AS (SELECT * FROM u) SELECT secret FROM c
ERR_MASK_UNSUPPORTED_CONTEXT SELECT x.id FROM (SELECT * FROM u JOIN main."semi" s1 ON s1.id = u.id) x
ERR_MASK_UNSUPPORTED_CONTEXT SELECT t.id FROM t WHERE t.s IN (SELECT u.* FROM u)
# EXISTS returns no columns, so a star that is its whole select list
# becomes the constant 1. Next to another item, or under a clause that
# reads the select list by position, the star stays a star.
same SELECT id FROM v WHERE EXISTS (SELECT * FROM u WHERE u.id = v.id AND u.a > 1) ORDER BY id
same SELECT id FROM v WHERE EXISTS (SELECT DISTINCT u.* FROM u WHERE u.id = v.id AND u.a > 1) ORDER BY id
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id FROM v WHERE EXISTS (SELECT *, unnest(l) FROM u WHERE u.id = v.id) ORDER BY id
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id FROM v WHERE EXISTS (SELECT * FROM u WHERE u.id = v.id ORDER BY 2 LIMIT 1) ORDER BY id
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id FROM v WHERE EXISTS (SELECT DISTINCT ON (2) * FROM u WHERE u.id = v.id) ORDER BY id
same SELECT id FROM u WHERE EXISTS (SELECT *, unnest(l) FROM t WHERE t.id = u.id) ORDER BY id
same SELECT id FROM u WHERE EXISTS (SELECT * FROM t WHERE t.id = u.id ORDER BY 2 LIMIT 1) ORDER BY id
same SELECT id FROM u WHERE EXISTS (SELECT *, t.a AS tc FROM t WHERE t.id = u.id AND tc > 1) ORDER BY id
same SELECT id FROM u WHERE EXISTS (SELECT DISTINCT ON (2) * FROM t WHERE t.id = u.id) ORDER BY id
same SELECT id FROM u WHERE EXISTS (SELECT * FROM v WHERE v.id = u.id GROUP BY 1, 2, 3, 4, 5, 6, 7) ORDER BY id
# A star that names a table of an outer SELECT, or that has no FROM, is
# refused when that table has a post-query mask. DuckDB does not expand
# such a star today, and the gateway does not rely on that.
ERR_MASK_UNSUPPORTED_CONTEXT SELECT x.secret FROM v, u, LATERAL (SELECT u.*) x
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id, (SELECT u.* FROM v LIMIT 1) FROM u
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id FROM u WHERE EXISTS (SELECT 1 FROM (SELECT u.* FROM v) x WHERE x.secret = 'u1')
ERR_MASK_UNSUPPORTED_CONTEXT SELECT x.secret FROM u, LATERAL (SELECT *) x
# The star is refused by its name: a STRUCT column of the nested SELECT
# called like the outer table is refused with it.
ERR_MASK_UNSUPPORTED_CONTEXT SELECT id FROM u WHERE id IN (SELECT u.* FROM (SELECT struct_pack(a := 1) AS u) x) ORDER BY id
same SELECT id FROM u WHERE id IN (SELECT (s).a FROM (SELECT struct_pack(a := 1) AS s) x) ORDER BY id
# When the nested SELECT reads a table with a post-query mask itself, star
# expansion refuses the unknown qualifier first.
ERR_UNSUPPORTED_SYNTAX SELECT id, (SELECT u.* FROM u d LIMIT 1) FROM u
# A bare star with a FROM stays and is served. DuckDB expands it over v
# alone. If it ever covered the outer table too, the rawfails line would
# serve u.secret raw and fail here.
same SELECT u.id, x.id, x.a FROM u, LATERAL (SELECT * FROM v WHERE v.id = u.id) x ORDER BY u.id
rawfails SELECT x.secret FROM u, LATERAL (SELECT * FROM v WHERE v.id = u.id) x
# As the whole select list of EXISTS both stars become the constant 1.
rawfails SELECT id FROM u WHERE EXISTS (SELECT u.*) ORDER BY id
rawfails SELECT id FROM u WHERE EXISTS (SELECT *) ORDER BY id
# Over a table with a SQL mask these stars would read the wrapper. DuckDB
# refuses them today.
rawfails SELECT x.secret FROM v, t, LATERAL (SELECT t.*) x
rawfails SELECT x.secret FROM t, LATERAL (SELECT *) x
rawfails SELECT to_json(t.*) FROM t
rawfails SELECT row(t.*) FROM t
rawfails SELECT struct_pack(t.*) FROM t
# A star over a FROM item of its own SELECT stays served, also under the
# name of the outer table.
same SELECT id FROM u WHERE id IN (SELECT x.* FROM (SELECT id FROM v) x) ORDER BY id
same SELECT id FROM u WHERE id IN (SELECT u.* FROM (SELECT id FROM v) u) ORDER BY id
same WITH c AS (SELECT id FROM v) SELECT id FROM u WHERE id IN (SELECT c.* FROM c) ORDER BY id

# v.secret_n, constant mask 2.
same SELECT id, secret_n FROM v ORDER BY id
same SELECT id, secret_n + 1, secret_n * a FROM v ORDER BY id
same SELECT id FROM v WHERE secret_n = 11 ORDER BY id
same SELECT id FROM v WHERE secret_n > 10 OR secret_n IS NULL ORDER BY id
same SELECT sum(secret_n), max(secret_n), count(DISTINCT secret_n) FROM v
same SELECT id, secret_n AS x, x + 1 FROM v ORDER BY id
same SELECT * FROM v ORDER BY id
same SELECT id, row_number() OVER (ORDER BY secret_n, id) FROM v ORDER BY id
same SELECT id, sum(a) OVER (PARTITION BY secret_n ORDER BY id) FROM v ORDER BY id
same SELECT id, rank() OVER (PARTITION BY secret_n ORDER BY secret_n, id) FROM v ORDER BY id
same SELECT id, secret_n FROM v ORDER BY secret_n + 0, id
same SELECT secret_n + 0 AS k, count(*) FROM v GROUP BY secret_n + 0
same SELECT id, CASE secret_n WHEN 11 THEN 'leak' ELSE 'ok' END FROM v ORDER BY id
same SELECT t.id FROM t JOIN v ON v.secret_n = t.id + 10 ORDER BY t.id

# A whole sort, group or DISTINCT ON key that is a constant mask. The key
# names the wrapper column, not the constant, so DuckDB does not read the
# integer 2 as column position 2.
same SELECT DISTINCT ON (secret) secret, id FROM t ORDER BY secret, id
same SELECT id FROM t ORDER BY secret, id
same SELECT id, a FROM v ORDER BY secret_n, id
same SELECT id, a, secret_n FROM v ORDER BY secret_n DESC, id
same SELECT secret_n, count(*) FROM v GROUP BY secret_n
same SELECT id, a, count(*) FROM v GROUP BY ROLLUP (secret_n, id, a)
same SELECT DISTINCT ON (secret_n) secret_n, id FROM v ORDER BY secret_n, id
same SELECT id FROM v ORDER BY secret_n, id LIMIT 2

# Tables named like a DuckDB keyword need an alias: the wrapper is named
# after the table, and DuckDB reads (..) semi JOIN as a SEMI JOIN.
ERR_UNSUPPORTED_SYNTAX SELECT count(*) FROM main."semi" JOIN u ON true
ERR_UNSUPPORTED_SYNTAX SELECT count(*) FROM main."by"
ERR_UNSUPPORTED_SYNTAX SELECT u.id FROM main."semi", u ORDER BY u.id
same SELECT count(*) FROM main."semi" s JOIN u ON true
same SELECT count(*) FROM main."by" b
same SELECT s.id, b.a FROM main."semi" s JOIN main."by" b ON b.id = s.id ORDER BY s.id
ERR_SYNTAX SELECT count(*) FROM semi

# Window frames over an interval go through the normalizer.
same SELECT id, sum(a) OVER (ORDER BY d RANGE BETWEEN INTERVAL '30' DAY PRECEDING AND CURRENT ROW) FROM t ORDER BY id
same SELECT id, count(secret) OVER (ORDER BY d RANGE BETWEEN INTERVAL '400' DAY PRECEDING AND CURRENT ROW) FROM t ORDER BY id
