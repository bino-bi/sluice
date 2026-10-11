# Literals. pg_query 17 reads 0x1f, 0o17 and 0b101 as integers. DuckDB reads
# 0x1f as 0 with the alias x1f, so the check refuses them at the end of a
# select item. Anywhere else DuckDB cannot parse them. Hint: write the
# decimal value.
ERR_UNSUPPORTED_SYNTAX SELECT 0x1f FROM t WHERE id = 1
ERR_UNSUPPORTED_SYNTAX SELECT 0o17 FROM t WHERE id = 1
ERR_UNSUPPORTED_SYNTAX SELECT -0x10 FROM t WHERE id = 1
ERR_UNSUPPORTED_SYNTAX SELECT id, 0xff FROM t ORDER BY id
ERR_SYNTAX SELECT 0x1f + 1 FROM t WHERE id = 1
ERR_SYNTAX SELECT 0x1f AS h FROM t WHERE id = 1
ERR_SYNTAX SELECT 0b101 AS b FROM t WHERE id = 1
ERR_SYNTAX SELECT id FROM t WHERE a = 0x1 ORDER BY id
ERR_SYNTAX SELECT id, CASE WHEN a > 0b1 THEN 1 END FROM t ORDER BY id
same SELECT 31, 5, 15, -16 FROM t WHERE id = 1

# DuckDB cannot parse U& strings, \u escapes in E strings, or a bare alias
# that is a DuckDB keyword.
ERR_SYNTAX SELECT E'\u0041' FROM t WHERE id = 1
ERR_SYNTAX SELECT E'\U0001F642' FROM t WHERE id = 1
ERR_SYNTAX SELECT id FROM t WHERE s = E'\u0041lice'
ERR_SYNTAX SELECT U&'d\0061t\+000061' FROM t WHERE id = 1
ERR_SYNTAX SELECT id, 1 at FROM t
ERR_SYNTAX SELECT id FROM t WHERE s = U&'bob'

# Number forms both parsers read alike.
same SELECT 1_000, 1e3, .5, 5., 1.5e-3, 123456789012, 9223372036854775807 FROM t WHERE id = 1
same SELECT id, a + 1_000, a * 1e3, a + .5 FROM t ORDER BY id
same SELECT id FROM t WHERE f > 1e3 ORDER BY id
same SELECT id FROM t WHERE n = 1.50 OR n = -2.25 ORDER BY id
same SELECT 170141183460469231731687303715884105727, -170141183460469231731687303715884105727 FROM t WHERE id = 1
same SELECT 0.1 + 0.2, 1 / 3, 7 // 2 FROM t WHERE id = 1
same SELECT -1, - 1, -(-1), +1 FROM t WHERE id = 1

# Strings.
same SELECT 'it''s', 'a' || 'b', '' FROM t WHERE id = 1
same SELECT E'a\nb', E'tab\there', E'quote\'s', E'back\\slash' FROM t WHERE id = 1
same SELECT E'\x41' FROM t WHERE id = 1
same SELECT E'\101' FROM t WHERE id = 1
same SELECT $$dollar 'quoted'$$, $tag$with $$ inside$tag$ FROM t WHERE id = 1
same SELECT id FROM t WHERE s = 'Alice' OR s = 'bob' ORDER BY id
same SELECT id FROM t WHERE s = E'x%y' ORDER BY id
same SELECT id, 'ABC', 'abc', 'Abc' FROM t ORDER BY id
same SELECT id FROM t WHERE s = 'ALICE' ORDER BY id
same SELECT 'ünï', 'emoji 🙂', '日本' FROM t WHERE id = 1
same SELECT id, s || ' ' || 'x' FROM t ORDER BY id

# Typed literals.
same SELECT DATE '2024-01-01', TIMESTAMP '2024-01-01 10:00:00', TIME '10:00:00' FROM t WHERE id = 1
same SELECT TIMESTAMPTZ '2024-01-01 10:00:00+02', TIMESTAMP WITH TIME ZONE '2024-01-01 10:00:00+02' FROM t WHERE id = 1
same SELECT id FROM t WHERE d >= DATE '2025-01-01' ORDER BY id
same SELECT id FROM t WHERE ts < TIMESTAMP '2024-07-01' ORDER BY id
same SELECT JSON '{"a":1}', JSON '[1,2]' FROM t WHERE id = 1
same SELECT id FROM t WHERE j = JSON '{"a":1}' ORDER BY id
same SELECT BOOLEAN 'true', INTEGER '42', VARCHAR 'x', DOUBLE '1.5' FROM t WHERE id = 1
same SELECT NUMERIC '1.25', DECIMAL '3.5' FROM t WHERE id = 1
same SELECT INTERVAL '1 day', INTERVAL '2 hours 3 minutes' FROM t WHERE id = 1

# NULL, booleans and arrays.
same SELECT NULL, NULL::INTEGER, true, false, NOT true FROM t WHERE id = 1
same SELECT id, a = NULL, a IS NULL, NULL IS NULL FROM t ORDER BY id
same SELECT ARRAY[1, 2, 3], ARRAY['a', 'b'], ARRAY[]::INTEGER[] FROM t WHERE id = 1
same SELECT (ARRAY[ARRAY[1, 2]])[1] FROM t WHERE id = 1
same SELECT id, ARRAY[a, b], ARRAY[a, NULL] FROM t ORDER BY id
same SELECT id FROM t WHERE a = ANY (ARRAY[1, 3]) ORDER BY id
same SELECT id, ROW(a, b) = ROW(1, 2), (a, s) = (1, 'Alice') FROM t ORDER BY id
same SELECT struct_pack(k := 1, v := 'x') FROM t WHERE id = 1

# DuckDB-only literal forms pg_query cannot parse.
ERR_SYNTAX SELECT {'k': 1, 'v': 'x'} FROM t WHERE id = 1
ERR_SYNTAX SELECT [1, 2] FROM t WHERE id = 1
ERR_SYNTAX SELECT ARRAY[ARRAY[1, 2]][1] FROM t WHERE id = 1
ERR_SYNTAX SELECT 'a' 'b' FROM t WHERE id = 1

# ARRAY[[..]]: pg_query and DuckDB build other trees (a pinned false refusal).
ERR_UNSUPPORTED_SYNTAX SELECT ARRAY[[1, 2], [3, 4]] FROM t WHERE id = 1
ERR_UNSUPPORTED_SYNTAX SELECT id, ARRAY[[a, b]] FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id FROM t WHERE ARRAY[[a]] = ARRAY[[1]] ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, CASE WHEN a > 0 THEN ARRAY[[a]] END FROM t ORDER BY id
