# Casts. pg_query qualifies json and bit with pg_catalog, which DuckDB does
# not know, and reads a bare FLOAT as float8 where DuckDB reads REAL. The
# normalizer drops the qualifier and writes float4.

# json and bit.
same SELECT id, j::json FROM t ORDER BY id
same SELECT '1'::json, CAST('[1]' AS json) FROM t WHERE id = 1
same SELECT id, ('[' || a || ']')::json FROM t ORDER BY id
same SELECT id, CAST(a AS json), ARRAY[j]::json[] FROM t ORDER BY id
same SELECT id FROM t WHERE j::json = '{"a":1}'::json ORDER BY id
same SELECT id, CASE WHEN j IS NULL THEN '{}'::json ELSE j END FROM t ORDER BY id
same SELECT CAST(1 AS bit), '101'::bit FROM t WHERE id = 1
same SELECT id, a::bit FROM t ORDER BY id
# DuckDB has no varbit type, but parses bit varying as varbit.
rawfails SELECT id, CAST(a AS bit varying) FROM t
rawfails SELECT '1010'::varbit FROM t
same SELECT id FROM t WHERE a::bit = 1::bit ORDER BY id
same SELECT count(*) FILTER (WHERE '{"x":1}'::json IS NOT NULL) FROM t
same SELECT id, (SELECT u.j::json FROM u WHERE u.id = t.id) FROM t ORDER BY id

# Bare FLOAT is REAL in DuckDB.
same SELECT id, CAST(a AS float) FROM t ORDER BY id
same SELECT id, a::float FROM t ORDER BY id
same SELECT id, f::float FROM t ORDER BY id
same SELECT 16777217::float FROM t WHERE id = 1
same SELECT id, f::FLOAT, f::Float FROM t ORDER BY id
same SELECT id, l::float[] FROM t ORDER BY id
same SELECT id FROM t WHERE f::float = 16777216 ORDER BY id
same SELECT id FROM t WHERE CAST(f AS float) > 1 ORDER BY id
same SELECT id, CASE WHEN f::float > 2 THEN 'big' END FROM t ORDER BY id
same SELECT sum(f::float) FILTER (WHERE id < 3) FROM t
same SELECT x.r FROM (SELECT f::float AS r FROM t) x ORDER BY x.r
same SELECT id, CAST(f AS float (53)) FROM t ORDER BY id
same SELECT id, CAST(f AS float(24)) FROM t ORDER BY id
same SELECT id, f::double precision, f::float8, f::double FROM t ORDER BY id
same SELECT id, f::real, f::float4 FROM t ORDER BY id
rawfails SELECT id, a::floaty FROM t
same SELECT id, a::float /* x */ + 1 FROM t ORDER BY id

# pg_catalog qualified types: the serializer drops the qualifier, raw fails
# at bind.
rawfails SELECT '12'::pg_catalog.int4 FROM t WHERE id = 1
rawfails SELECT id, a::pg_catalog.int8 FROM t

# Other types.
same SELECT id, n::numeric(12, 3), n::decimal(5, 1), a::numeric FROM t ORDER BY id
same SELECT id, s::varchar(3), s::char(2), s::text FROM t ORDER BY id
same SELECT id, ts::timestamp(0), ts::date, d::timestamp FROM t ORDER BY id
same SELECT id, ts::time, tz::timestamp FROM t ORDER BY id
same SELECT '12:00:00+02'::time with time zone, '12:00:00'::time without time zone FROM t WHERE id = 1
same SELECT id, ARRAY[1, 2, 3]::int[3], l::text[], l::varchar[] FROM t ORDER BY id
same SELECT id, (-1)::text, (-a)::text FROM t ORDER BY id
same SELECT id, a::varchar || 'x', a::boolean, flag::int FROM t ORDER BY id
same SELECT id, d::varchar, ts::varchar, iv::varchar, bl::varchar FROM t ORDER BY id
same SELECT id, st.x::varchar, j::varchar FROM t ORDER BY id
same SELECT id, '2024-01-01'::date + a FROM t ORDER BY id
same SELECT id, CAST(iv AS varchar), CAST(n AS double), CAST(f AS decimal(18, 2)) FROM t ORDER BY id
same SELECT id, a::smallint, a::bigint, a::hugeint, a::tinyint, a::int2, a::int8 FROM t ORDER BY id
same SELECT id, a::ubigint, a::uinteger FROM t WHERE a >= 0 ORDER BY id
same SELECT id, s::blob, 'abc'::bytea FROM t ORDER BY id
same SELECT id, a::integer::varchar::integer FROM t ORDER BY id
same SELECT id FROM t WHERE a::varchar = '1' ORDER BY id
same SELECT id FROM t WHERE d::timestamp < TIMESTAMP '2025-01-01' ORDER BY id
same SELECT id, CAST(a AS INTEGER) + CAST(b AS BIGINT) FROM t ORDER BY id
same SELECT id, CAST(st AS VARCHAR), CAST(l AS VARCHAR) FROM t ORDER BY id
same SELECT id, CAST(j ->> 'b' AS INTEGER) FROM t ORDER BY id
same SELECT id, '{"a": 1}'::json ->> 'a' FROM t WHERE id = 1
# Both parsers read -1::text as -(1::text).
rawfails SELECT id, -1::text FROM t
rawfails SELECT id, a::timestamp FROM t
rawfails SELECT id, CAST(s AS INTEGER) FROM t
rawfails SELECT id, s::date FROM t

# TRY_CAST is DuckDB only.
ERR_SYNTAX SELECT id, TRY_CAST(s AS INTEGER) FROM t ORDER BY id
