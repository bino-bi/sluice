# Ordinary analyst statements. None of them hits a known divergence, so all
# must be served with the expected rows.

# Projections and filters.
same SELECT id, a, s FROM t ORDER BY id
same SELECT * FROM t ORDER BY id
same SELECT * FROM u ORDER BY id
same SELECT * FROM v ORDER BY id
same SELECT count(*) FROM t
same SELECT count(*), count(a), count(DISTINCT a) FROM t
same SELECT id, a + b AS total FROM t WHERE a > 0 ORDER BY id
same SELECT id FROM t WHERE a IS NULL OR b IS NULL ORDER BY id
same SELECT id FROM t WHERE a BETWEEN 1 AND 2 AND b <> 3 ORDER BY id
same SELECT id FROM t WHERE s IN ('Alice', 'bob') ORDER BY id
same SELECT id FROM t WHERE s NOT IN ('Alice') ORDER BY id
same SELECT id FROM t WHERE flag ORDER BY id
same SELECT id FROM t WHERE NOT flag ORDER BY id
same SELECT id FROM t WHERE flag IS NOT TRUE ORDER BY id
same SELECT id FROM t WHERE d >= DATE '2025-01-01' AND d < DATE '2026-01-01' ORDER BY id
same SELECT id FROM t WHERE ts::DATE = DATE '2024-01-15' ORDER BY id
same SELECT id FROM t WHERE n > 1 ORDER BY id
same SELECT id FROM t WHERE f < 1 ORDER BY id
same SELECT id, s FROM t WHERE lower(s) LIKE '%b%' ORDER BY id
same SELECT DISTINCT a FROM t
same SELECT DISTINCT a, b FROM t
same SELECT id FROM t ORDER BY a DESC NULLS LAST, id
same SELECT id FROM t ORDER BY a NULLS FIRST, id
same SELECT id, a FROM t ORDER BY 2 DESC, 1
same SELECT id FROM t ORDER BY id LIMIT 3
same SELECT id FROM t ORDER BY id LIMIT 2 OFFSET 2
same SELECT id FROM t ORDER BY id OFFSET 4
same SELECT id FROM t ORDER BY id FETCH FIRST 2 ROWS ONLY

# Aggregates and grouping.
same SELECT a, count(*) FROM t GROUP BY a
same SELECT a, count(*) AS n FROM t GROUP BY a HAVING count(*) > 1
same SELECT flag, sum(a), avg(b), min(d), max(ts) FROM t GROUP BY flag
same SELECT EXTRACT(year FROM d) AS y, count(*) FROM t GROUP BY y
same SELECT date_trunc('year', d) AS y, sum(n) FROM t GROUP BY 1
same SELECT a, b, count(*) FROM t GROUP BY GROUPING SETS ((a), (b), ())
same SELECT a, b, count(*) FROM t GROUP BY ROLLUP (a, b)
same SELECT a, flag, count(*) FROM t GROUP BY CUBE (a, flag)
same SELECT a, grouping(a), count(*) FROM t GROUP BY ROLLUP (a)
same SELECT count(*) FILTER (WHERE flag), count(*) FILTER (WHERE NOT flag) FROM t
same SELECT string_agg(s, ',' ORDER BY id) FROM t
same SELECT list(a ORDER BY id), array_agg(b ORDER BY id) FROM t
same SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY a) FROM t
same SELECT percentile_disc(0.5) WITHIN GROUP (ORDER BY b) FROM t
same SELECT median(a), quantile_cont(b, 0.25) FROM t
same SELECT bool_and(flag), bool_or(flag) FROM t
same SELECT sum(n), avg(n), sum(f) FROM t
same SELECT arg_max(s, a), arg_min(s, b) FROM t WHERE a <> 1 AND b <> 1
same SELECT count(*) FROM t WHERE a > (SELECT avg(a) FROM u)
same SELECT a, count(DISTINCT b) FROM t GROUP BY a HAVING count(DISTINCT b) >= 1
same SELECT stddev_samp(a), var_pop(b) FROM t

# Joins.
same SELECT t.id, u.s FROM t JOIN u ON u.id = t.id ORDER BY t.id
same SELECT t.id, u.id FROM t LEFT JOIN u ON u.a = t.a ORDER BY t.id, u.id
same SELECT t.id, u.id FROM t RIGHT JOIN u ON u.a = t.b ORDER BY u.id, t.id
same SELECT t.id, u.id FROM t FULL JOIN u ON u.a = t.a AND u.id = t.id ORDER BY t.id, u.id
same SELECT t.id, u.id FROM t CROSS JOIN u WHERE t.id + u.id = 7 ORDER BY t.id, u.id
same SELECT t.id, v.secret_n FROM t JOIN v USING (id) ORDER BY t.id
same SELECT id, t.a, v.a FROM t JOIN v USING (id) ORDER BY id
same SELECT x.id, y.id FROM t x JOIN t y ON y.a = x.b ORDER BY x.id, y.id
same SELECT t.id FROM t, u WHERE t.id = u.id AND u.flag ORDER BY t.id
same SELECT t.id, u.id, v.id FROM t JOIN u ON u.id = t.id JOIN v ON v.id = u.id ORDER BY t.id
same SELECT t.id FROM t LEFT JOIN u ON u.id = t.id + 3 WHERE u.id IS NULL ORDER BY t.id
same SELECT t.id, x.n FROM t, LATERAL (SELECT count(*) AS n FROM u WHERE u.id <= t.id) x ORDER BY t.id
same SELECT t.id, g.i FROM t, generate_series(1, 2) AS g(i) ORDER BY t.id, g.i
same SELECT t.id, r.range FROM t CROSS JOIN range(2) r ORDER BY t.id, r.range
same SELECT t.id, x.e FROM t, unnest(ARRAY[10, 20]) AS x(e) ORDER BY t.id, x.e

# Subqueries.
same SELECT id FROM t WHERE id IN (SELECT id FROM u WHERE flag) ORDER BY id
same SELECT id FROM t WHERE id NOT IN (SELECT id FROM u WHERE a IS NOT NULL) ORDER BY id
same SELECT id FROM t WHERE EXISTS (SELECT 1 FROM u WHERE u.id = t.id AND u.a > 1) ORDER BY id
same SELECT id FROM t WHERE NOT EXISTS (SELECT 1 FROM v WHERE v.id = t.id) ORDER BY id
same SELECT id, (SELECT count(*) FROM u WHERE u.a = t.a) AS n FROM t ORDER BY id
same SELECT id FROM t WHERE a = ANY (SELECT b FROM u) ORDER BY id
same SELECT id FROM t WHERE a > ALL (SELECT a FROM u WHERE a IS NOT NULL) ORDER BY id
same SELECT x.a, x.n FROM (SELECT a, count(*) AS n FROM t GROUP BY a) x WHERE x.n > 1 ORDER BY x.a
same SELECT max(x.total) FROM (SELECT id, a + b AS total FROM t) x
same SELECT (SELECT max(id) FROM t) - (SELECT min(id) FROM u)
same SELECT id FROM (SELECT * FROM (SELECT * FROM t) y WHERE y.a > 0) z ORDER BY id

# CTEs and set operations.
same WITH c AS (SELECT id, a FROM t WHERE a > 0) SELECT * FROM c ORDER BY id
same WITH c AS (SELECT a, count(*) AS n FROM t GROUP BY a), d AS (SELECT max(n) AS m FROM c) SELECT c.a FROM c, d WHERE c.n = d.m ORDER BY c.a
same WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM r WHERE n < (SELECT count(*) FROM t)) SELECT sum(n) FROM r
same WITH c AS MATERIALIZED (SELECT id FROM t) SELECT count(*) FROM c
same SELECT id FROM t UNION SELECT id FROM v
same SELECT id FROM t UNION ALL SELECT id FROM v
same SELECT a FROM t INTERSECT SELECT a FROM v
same SELECT a FROM t EXCEPT SELECT a + 1 FROM v
same SELECT id FROM t UNION ALL SELECT id FROM v ORDER BY id
same (SELECT id FROM t ORDER BY id LIMIT 2) UNION ALL (SELECT id FROM v ORDER BY id LIMIT 1)

# Window functions.
same SELECT id, row_number() OVER (ORDER BY id) FROM t ORDER BY id
same SELECT id, rank() OVER (PARTITION BY flag ORDER BY a, id) FROM t ORDER BY id
same SELECT id, sum(a) OVER (ORDER BY id ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) FROM t ORDER BY id
same SELECT id, avg(b) OVER (PARTITION BY a) FROM t ORDER BY id
same SELECT id, lag(a) OVER (ORDER BY id), lead(a, 2, 0) OVER (ORDER BY id) FROM t ORDER BY id
same SELECT id, first_value(s) OVER w, last_value(s) OVER w FROM t WINDOW w AS (ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) ORDER BY id
same SELECT id, ntile(2) OVER (ORDER BY id), percent_rank() OVER (ORDER BY id) FROM t ORDER BY id
same SELECT id, count(*) OVER () FROM t ORDER BY id
same SELECT id, sum(a) FILTER (WHERE flag) OVER (ORDER BY id) FROM t ORDER BY id
same SELECT id, dense_rank() OVER (ORDER BY a DESC NULLS LAST, id) FROM t ORDER BY id

# Expressions and functions.
same SELECT id, CASE WHEN a > 1 THEN 'hi' WHEN a = 1 THEN 'one' ELSE 'lo' END FROM t ORDER BY id
same SELECT id, CASE a WHEN 1 THEN 'x' WHEN 2 THEN 'y' END FROM t ORDER BY id
same SELECT id, coalesce(a, b, 0), nullif(a, 1), greatest(a, b), least(a, b) FROM t ORDER BY id
same SELECT id, upper(s), lower(s), length(s), reverse(s), left(s, 2), right(s, 2) FROM t ORDER BY id
same SELECT id, concat(s, '-', a), s || '-' || a, concat_ws('/', s, a) FROM t ORDER BY id
same SELECT id, replace(s, 'b', 'B'), regexp_replace(s, '[aeiou]', '*', 'g'), split_part(s, '_', 1) FROM t ORDER BY id
same SELECT id, lpad(s, 6, '.'), rpad(s, 6, '.'), repeat(s, 2) FROM t ORDER BY id
same SELECT id, abs(n), round(n, 1), ceil(f), floor(f), sign(a) FROM t ORDER BY id
same SELECT id, a / NULLIF(b, 0), a % NULLIF(b, 0) FROM t ORDER BY id
same SELECT id, len(l), list_contains(l, 2), l[1], list_sort(l), list_sum(l) FROM t ORDER BY id
same SELECT id, st.x, st.y, struct_extract(st, 'x') FROM t ORDER BY id
same SELECT id, j ->> 'a', json_extract(j, '$.b'), json_type(j) FROM t ORDER BY id
same SELECT id, unnest(l) FROM t ORDER BY id, 2
same SELECT id, octet_length(bl), bl FROM t ORDER BY id
same SELECT id, date_diff('day', d, DATE '2026-12-31'), d + 30 FROM t ORDER BY id
same SELECT id, age(TIMESTAMP '2026-01-01', ts) FROM t ORDER BY id
same SELECT id, strftime(ts, '%Y-%m-%d %H'), epoch(ts) FROM t ORDER BY id
same SELECT id, ts::DATE, date_trunc('week', ts) FROM t ORDER BY id
same SELECT id, ifnull(s, '?'), if(flag, 'y', 'n') FROM t ORDER BY id
same SELECT id, hash(s) = hash(s), md5(s) FROM t ORDER BY id

# Functions outside the allowlist.
ACL_REJECTED SELECT json_object('a', 1) FROM t
ACL_REJECTED SELECT id FROM t WHERE json_object('a', a) IS NOT NULL
ACL_REJECTED SELECT id, CASE WHEN a > 0 THEN json_object('a', a) END FROM t
ACL_REJECTED SELECT id, current_setting('threads') FROM t
ACL_REJECTED SELECT id, typeof(a) FROM t

# DuckDB-only syntax pg_query cannot parse.
ERR_SYNTAX SELECT t.id FROM t SEMI JOIN u ON u.id = t.id ORDER BY t.id
ERR_SYNTAX SELECT t.id FROM t ANTI JOIN u ON u.a = t.a ORDER BY t.id
ERR_SYNTAX SELECT s FROM t UNION BY NAME SELECT s FROM v
ERR_SYNTAX SELECT id FROM t QUALIFY row_number() OVER (ORDER BY id) <= 2 ORDER BY id
ERR_SYNTAX SELECT * EXCLUDE (secret) FROM t ORDER BY id
ERR_SYNTAX FROM t SELECT id ORDER BY id
ERR_SYNTAX SELECT id FROM t ORDER BY ALL
ERR_SYNTAX SELECT a, count(*) FROM t GROUP BY ALL
