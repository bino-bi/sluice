# Mask bypass attempts. t.secret has the constant mask '***', u.secret a
# post-query mask, v.secret_n the constant mask 2. Commit 3 moves SQL masks
# into the table wrapper; the constant mask lines are grouped below so they
# are easy to update.

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
same SELECT id, secret AS x FROM t WHERE x = 's1' ORDER BY id
same SELECT id, secret AS x FROM t ORDER BY x, id
same SELECT secret AS x, count(*) FROM t GROUP BY x
same SELECT id, secret AS x, (SELECT x) FROM t ORDER BY id
same SELECT id, (SELECT secret) FROM t ORDER BY id
same SELECT id, (SELECT t.secret FROM u WHERE u.id = t.id) FROM t ORDER BY id
same SELECT id, (SELECT max(secret) FROM t) FROM u ORDER BY id
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

# A whole sort, group or DISTINCT ON key that is a constant mask. The mask
# is inlined: DuckDB reads the integer 2 as column position 2, and refuses a
# string constant there. Commit 3 fixes this.
knownbug-commit3 SELECT DISTINCT ON (secret) secret, id FROM t ORDER BY secret, id
knownbug-commit3 SELECT id FROM t ORDER BY secret, id
knownbug-commit3 SELECT id, a FROM v ORDER BY secret_n, id
knownbug-commit3 SELECT id, a, secret_n FROM v ORDER BY secret_n DESC, id
knownbug-commit3 SELECT secret_n, count(*) FROM v GROUP BY secret_n
knownbug-commit3 SELECT id, a, count(*) FROM v GROUP BY ROLLUP (secret_n, id, a)
knownbug-commit3 SELECT DISTINCT ON (secret_n) secret_n, id FROM v ORDER BY secret_n, id
knownbug-commit3 SELECT id FROM v ORDER BY secret_n, id LIMIT 2

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
