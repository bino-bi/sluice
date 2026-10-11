# LIKE and ILIKE with ESCAPE. pg_query reads x LIKE p ESCAPE e as
# x ~~ like_escape(p, e), which DuckDB has no 2-argument form of. The
# normalizer writes DuckDB's like_escape(x, p, e) and its 3 siblings.

# Select list.
same SELECT id, s LIKE 'a!_%' ESCAPE '!' FROM t ORDER BY id
same SELECT id, s NOT LIKE 'a!_%' ESCAPE '!' FROM t ORDER BY id
same SELECT id, s ILIKE 'A!_B' ESCAPE '!' FROM t ORDER BY id
same SELECT id, s NOT ILIKE 'a!_b' ESCAPE '!' FROM t ORDER BY id
same SELECT id, s LIKE '%#%%' ESCAPE '#' FROM t ORDER BY id
same SELECT id, s LIKE 'A!!!_B' ESCAPE '!' FROM t ORDER BY id
same SELECT id, s ILIKE '%$_%' ESCAPE '$' FROM t ORDER BY id
same SELECT id, upper(s) LIKE 'A!_%' ESCAPE '!' FROM t ORDER BY id
same SELECT id, s || 'x' LIKE '%!_bx' ESCAPE '!' FROM t ORDER BY id
same SELECT id, (s LIKE 'a!_%' ESCAPE '!') IS NULL FROM t ORDER BY id
same SELECT id, NOT s LIKE 'x!%y' ESCAPE '!' FROM t ORDER BY id

# WHERE.
same SELECT id FROM t WHERE s LIKE 'x#%y' ESCAPE '#' ORDER BY id
same SELECT id FROM t WHERE s NOT LIKE 'a!_b' ESCAPE '!' ORDER BY id
same SELECT id FROM t WHERE s ILIKE 'a!_%' ESCAPE '!' ORDER BY id
same SELECT id FROM t WHERE s NOT ILIKE '%!_%' ESCAPE '!' ORDER BY id
same SELECT id FROM t WHERE s LIKE 'A!!!_B' ESCAPE '!' ORDER BY id
same SELECT id FROM t WHERE s LIKE 'a!_%' ESCAPE '!' OR s ILIKE 'B%' ORDER BY id
same SELECT id FROM t WHERE flag AND s NOT LIKE '%!%%' ESCAPE '!' ORDER BY id
same SELECT id FROM t WHERE (s LIKE 'a!_%' ESCAPE '!') IS NOT TRUE ORDER BY id

# Nested.
same SELECT id, CASE WHEN s ILIKE '%!_%' ESCAPE '!' THEN 'u' ELSE 'n' END FROM t ORDER BY id
same SELECT count(*) FILTER (WHERE s LIKE '%!%%' ESCAPE '!') FROM t
same SELECT id FROM t WHERE id IN (SELECT id FROM u WHERE s LIKE 'A!_%' ESCAPE '!') ORDER BY id
same SELECT id, count(*) FILTER (WHERE s NOT ILIKE 'a!_b' ESCAPE '!') OVER (ORDER BY id) FROM t ORDER BY id
same WITH c AS (SELECT id FROM t WHERE s ILIKE 'x!%Y' ESCAPE '!') SELECT id FROM c ORDER BY id
same SELECT id, (SELECT count(*) FROM u WHERE u.s LIKE t.s || '!_%' ESCAPE '!') FROM t ORDER BY id

# Without ESCAPE, and the operator forms.
same SELECT id, s LIKE 'a_b' FROM t ORDER BY id
same SELECT id, s ILIKE 'alice' FROM t ORDER BY id
same SELECT id, s NOT LIKE '%b' FROM t ORDER BY id
same SELECT id, s NOT ILIKE '%B' FROM t ORDER BY id
same SELECT id FROM t WHERE s LIKE '%' || 'b' ORDER BY id
same SELECT id, s ~~ 'a%' FROM t ORDER BY id
same SELECT id, s !~~ 'a%' FROM t ORDER BY id
same SELECT id, s ~~* 'A%' FROM t ORDER BY id
same SELECT id, s !~~* 'A%' FROM t ORDER BY id
same SELECT id FROM t WHERE s LIKE '_' || '%' ORDER BY id
same SELECT id, s LIKE NULL FROM t ORDER BY id

# SIMILAR TO round-trips.
same SELECT id, s SIMILAR TO 'a_b' FROM t ORDER BY id
same SELECT id, s NOT SIMILAR TO 'A.*' FROM t ORDER BY id
same SELECT id FROM t WHERE s SIMILAR TO '[a-z]+' ORDER BY id

# Function forms.
same SELECT id, like_escape(s, 'a!_%', '!') FROM t ORDER BY id
same SELECT id, ilike_escape(s, 'A!_B', '!') FROM t ORDER BY id
same SELECT id, starts_with(s, 'a'), contains(s, '_') FROM t ORDER BY id
same SELECT id, regexp_matches(s, '^a') FROM t ORDER BY id
same SELECT id FROM t WHERE regexp_matches(s, '[%_]') ORDER BY id
same SELECT id, prefix(s, 'A'), suffix(s, 'b') FROM t ORDER BY id

# An explicit pg_catalog.like_escape stays as written and fails like raw.
rawfails SELECT id, s ~~ pg_catalog.like_escape('a!_%', '!') FROM t

# GLOB is DuckDB only.
ERR_SYNTAX SELECT id FROM t WHERE s GLOB 'a*'
