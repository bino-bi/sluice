# DuckDB operators pg_query reads with another precedence: ** sits at the ^
# level, // at the * level, the lambda -> binds looser than OR, and ->> binds
# looser than AND, OR and NOT. Hints: pow, divide, x -> (x > 1),
# (j ->> 'a') = '1'.

# Select list.
ERR_UNSUPPORTED_SYNTAX SELECT 2 * 3 ** 2 FROM t WHERE id = 1
ERR_UNSUPPORTED_SYNTAX SELECT id, a * b ** 2 FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, b // 2 + 1 FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, a + b // 2 FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, list_filter(l, x -> x > 1) FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, list_transform(l, x -> x + 1 = 2) FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, flag AND j ->> 'a' = '1' FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, NOT j ->> 'a' = '1' FROM t ORDER BY id

# WHERE.
ERR_UNSUPPORTED_SYNTAX SELECT id FROM t WHERE b // 2 + 1 = 2 ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id FROM t WHERE a * 2 ** 2 > 4 ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id FROM t WHERE flag AND j ->> 'a' = '1' ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id FROM t WHERE flag OR j ->> 'a' = 'x' ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id FROM t WHERE len(list_filter(l, x -> x > 1)) > 0 ORDER BY id

# Nested.
ERR_UNSUPPORTED_SYNTAX SELECT id, CASE WHEN a // 2 + 1 = 1 THEN 'y' END FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT count(*) FILTER (WHERE flag AND j ->> 'a' = '1') FROM t
ERR_UNSUPPORTED_SYNTAX SELECT id, (SELECT list_filter(l, x -> x > 1)) FROM t ORDER BY id

# The hints.
same SELECT 2 * pow(3, 2) FROM t WHERE id = 1
same SELECT id, a * pow(b, 2), power(a, 2) FROM t ORDER BY id
same SELECT id, divide(b, 2) + 1 FROM t ORDER BY id
same SELECT id, list_filter(l, x -> (x > 1)) FROM t ORDER BY id
same SELECT id, list_transform(l, x -> (x + 1 = 2)) FROM t ORDER BY id
same SELECT id FROM t WHERE flag AND (j ->> 'a') = '1' ORDER BY id
same SELECT id, NOT ((j ->> 'a') = '1') FROM t ORDER BY id
same SELECT id FROM t WHERE len(list_filter(l, x -> (x > 1))) > 0 ORDER BY id

# A lone operator has nothing to group with.
same SELECT id, a ** 2 FROM t ORDER BY id
same SELECT id, b // 2 FROM t ORDER BY id
same SELECT id, j ->> 'a' FROM t ORDER BY id
same SELECT id, j -> 'a' FROM t ORDER BY id
same SELECT id, list_transform(l, x -> x * 2) FROM t ORDER BY id
same SELECT id, list_apply(l, x -> x + a) FROM t ORDER BY id

# Precedence both parsers share.
same SELECT id, a + b * 2, (a + b) * 2, a - b - 1, a / 2 * 2 FROM t ORDER BY id
same SELECT id, a % 2, -a % 2, -(a % 2) FROM t ORDER BY id
same SELECT id, 2 ^ 3 ^ 2, -2 ^ 2, (-2) ^ 2 FROM t WHERE id = 1
same SELECT id, a || b || s, s || 1 + 1 FROM t ORDER BY id
same SELECT id, a = 1 AND b = 2 OR flag, a = 1 AND (b = 2 OR flag) FROM t ORDER BY id
same SELECT id, NOT a = 1, NOT flag AND a = 1, NOT (flag AND a = 1) FROM t ORDER BY id
same SELECT id, (a < b) = true, a + 1 BETWEEN 1 AND 3 FROM t ORDER BY id
same SELECT id, (-a)::VARCHAR, -(a::VARCHAR::INTEGER) FROM t ORDER BY id
rawfails SELECT id, -a::VARCHAR FROM t
same SELECT id, a::VARCHAR || 'x', s || a::VARCHAR FROM t ORDER BY id
same SELECT id, ts AT TIME ZONE 'UTC', (ts + INTERVAL '1' HOUR) AT TIME ZONE 'UTC' FROM t ORDER BY id
same SELECT id, tz AT TIME ZONE 'Europe/Berlin' AT TIME ZONE 'UTC' FROM t ORDER BY id
same SELECT id, s COLLATE NOCASE = 'ALICE' FROM t ORDER BY id
same SELECT id FROM t ORDER BY s COLLATE NOCASE, id
same SELECT id, a IS NOT DISTINCT FROM b, a IS DISTINCT FROM b + 1 FROM t ORDER BY id
same SELECT id, a & b, a | b, ~a, a << 1, b >> 1 FROM t ORDER BY id
ERR_SYNTAX SELECT id, a # b FROM t ORDER BY id
same SELECT id, l @> ARRAY[1], ARRAY[1] <@ l, l && ARRAY[2] FROM t ORDER BY id
same SELECT id, st.x + 1, (st).y FROM t ORDER BY id
same SELECT id, j -> 'a' ->> 0, j ->> 'b' FROM t ORDER BY id
same SELECT id FROM t WHERE (j ->> 'a') = '1' OR (j ->> 'a') = 'x' ORDER BY id
same SELECT id, l[1] + l[2], l[1:2] FROM t ORDER BY id
same SELECT id, f + 0.5 * 2, f / 3 FROM t ORDER BY id
same SELECT id, n * 2, n + 0.01, -n FROM t ORDER BY id
