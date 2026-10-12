# Dropped parentheses. pg_query deparses some parenthesized operands bare
# (IS [NOT] NULL, IS TRUE, the left side of IN and op ANY, BETWEEN), so DuckDB
# groups them another way. The check refuses them. Hint: coalesce(x) IS NULL.

# Select list.
ERR_UNSUPPORTED_SYNTAX SELECT id, (NOT flag) IS NULL FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, (a = 1 OR b = 2) IS NULL FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, (a = 1 AND b = 2) IS NOT NULL FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, (a = 1) IN (false) FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, (a = 1) NOT IN (true) FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, (a IS NOT DISTINCT FROM b) IN (true) FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, flag BETWEEN false AND (a = 1 OR b = 2) FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, (a IS DISTINCT FROM 1) IS NULL FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, (a = 1) BETWEEN false AND false FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, (a IS DISTINCT FROM b) IS TRUE FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, (a = 1) = ANY (ARRAY[true]) FROM t ORDER BY id

# WHERE.
ERR_UNSUPPORTED_SYNTAX SELECT id FROM t WHERE id = 3 OR (id = 1 OR id = 2) IS NULL ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id FROM t WHERE (NOT flag) IS NULL ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id FROM t WHERE (id = 1) BETWEEN false AND false ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id FROM t WHERE (a = 1) IN (false) ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id FROM t WHERE (a = 1 OR b = 1) IS NOT NULL ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id FROM t WHERE (a IS DISTINCT FROM b) IS TRUE ORDER BY id

# Nested.
ERR_UNSUPPORTED_SYNTAX SELECT id, CASE WHEN (a = 1 OR b = 2) IS NULL THEN 1 ELSE 0 END FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT count(*) FILTER (WHERE (NOT flag) IS NULL) FROM t
ERR_UNSUPPORTED_SYNTAX SELECT id FROM t WHERE id IN (SELECT id FROM u WHERE (a = 1) IN (false)) ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, sum(a) FILTER (WHERE (a IS DISTINCT FROM b) IS TRUE) OVER (ORDER BY id) FROM t ORDER BY id

# The hint: coalesce keeps the grouping.
same SELECT id, coalesce(NOT flag) IS NULL FROM t ORDER BY id
same SELECT id, coalesce(a = 1 OR b = 2) IS NULL FROM t ORDER BY id
same SELECT id FROM t WHERE coalesce(a = 1 OR b = 1) IS NOT NULL ORDER BY id
same SELECT id, CASE WHEN coalesce(a = 1 OR b = 2) IS NULL THEN 1 ELSE 0 END FROM t ORDER BY id

# Parentheses the deparse keeps, or does not need: a comparison binds
# tighter than IS, and a chain of IS tests groups to the left, in both
# parsers.
same SELECT id, (a < b) IS TRUE FROM t ORDER BY id
same SELECT id, (a = b) IS NOT FALSE FROM t ORDER BY id
same SELECT id, (a = 1) IS UNKNOWN FROM t ORDER BY id
same SELECT id FROM t WHERE (a > 1) IS TRUE ORDER BY id
same SELECT id, sum(a) FILTER (WHERE (a = b) IS NOT TRUE) OVER (ORDER BY id) FROM t ORDER BY id
same SELECT id, (a IS NULL) IS NOT TRUE FROM t ORDER BY id
same SELECT id, (flag IS NULL) IS NULL FROM t ORDER BY id
same SELECT id, sum(a) FILTER (WHERE (a IS NULL) IS NOT TRUE) OVER (ORDER BY id) FROM t ORDER BY id
same SELECT id, (a + b) * 2 FROM t ORDER BY id
same SELECT id, a - (b - 1) FROM t ORDER BY id
same SELECT id, -(a + b) FROM t ORDER BY id
same SELECT id, (a IS NULL) = (b IS NULL) FROM t ORDER BY id
same SELECT id, NOT (a = 1 AND b = 2) FROM t ORDER BY id
same SELECT id, (a = 1 OR b = 2) AND flag FROM t ORDER BY id
same SELECT id, a = 1 OR (b = 2 AND flag) FROM t ORDER BY id
same SELECT id, a IS NULL, b IS NOT NULL FROM t ORDER BY id
same SELECT id, (flag OR a > 1) IS TRUE FROM t ORDER BY id
same SELECT id, (ARRAY[1, 2, 3])[2] FROM t ORDER BY id
same SELECT id, (l)[1] FROM t ORDER BY id
same SELECT id, a BETWEEN 1 AND 2 FROM t ORDER BY id
same SELECT id, a NOT BETWEEN b AND b + 2 FROM t ORDER BY id
same SELECT id, a IN (1, 2) FROM t ORDER BY id
same SELECT id, (a + 1) IN (2, 3) FROM t ORDER BY id
same SELECT id, (a + b) BETWEEN 1 AND 3 FROM t ORDER BY id
same SELECT id, a = ANY (ARRAY[1, 2]) FROM t ORDER BY id
same SELECT id, (a > 1) = (b > 1) FROM t ORDER BY id
same SELECT id, (a * 2) % 3, a * (2 % 3) FROM t ORDER BY id
same SELECT id, (s || 'x') = 'bobx' FROM t ORDER BY id
same SELECT id FROM t WHERE (a = 1 OR b = 1) AND (flag OR flag IS NULL) ORDER BY id
same SELECT id FROM t WHERE NOT (a IN (1, 2)) ORDER BY id
same SELECT id FROM t WHERE (a, b) = (1, 2) ORDER BY id
same SELECT id FROM t WHERE (a, b) IN ((1, 2), (2, 1)) ORDER BY id
