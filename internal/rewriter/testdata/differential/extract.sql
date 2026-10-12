# EXTRACT and the other SQL syntax functions.

# Singular units: both parsers read them alike.
same SELECT id, EXTRACT(year FROM d), EXTRACT(month FROM d), EXTRACT(day FROM d) FROM t ORDER BY id
same SELECT id, EXTRACT(hour FROM ts), EXTRACT(minute FROM ts), EXTRACT(second FROM ts) FROM t ORDER BY id
same SELECT id, EXTRACT(dow FROM d), EXTRACT(doy FROM d), EXTRACT(isodow FROM d) FROM t ORDER BY id
same SELECT id, EXTRACT(quarter FROM d), EXTRACT(week FROM d), EXTRACT(epoch FROM ts) FROM t ORDER BY id
same SELECT id, EXTRACT(decade FROM d), EXTRACT(century FROM d), EXTRACT(millennium FROM d) FROM t ORDER BY id
same SELECT id, EXTRACT(millisecond FROM ts), EXTRACT(microsecond FROM ts) FROM t ORDER BY id
same SELECT id, EXTRACT(day FROM iv), EXTRACT(hour FROM iv), EXTRACT(month FROM iv) FROM t ORDER BY id
same SELECT id, EXTRACT(YEAR FROM d), EXTRACT(Month FROM d) FROM t ORDER BY id
same SELECT id, EXTRACT(year FROM tz), EXTRACT(timezone_hour FROM tz) FROM t ORDER BY id
same SELECT id FROM t WHERE EXTRACT(year FROM d) = 2025 ORDER BY id
same SELECT id FROM t WHERE EXTRACT(month FROM d) BETWEEN 1 AND 6 ORDER BY id
same SELECT id FROM t WHERE EXTRACT(hour FROM ts) > 7 AND EXTRACT(dow FROM ts) <> 0 ORDER BY id
same SELECT EXTRACT(year FROM d), count(*) FROM t GROUP BY EXTRACT(year FROM d)
same SELECT id, CASE WHEN EXTRACT(month FROM d) > 6 THEN 'h2' ELSE 'h1' END FROM t ORDER BY id
same SELECT count(*) FILTER (WHERE EXTRACT(year FROM d) = 2024) FROM t
same SELECT id, (SELECT max(EXTRACT(day FROM u.d)) FROM u WHERE u.id <= t.id) FROM t ORDER BY id

# A unit that is not a DuckDB keyword keeps its spelling in DuckDB's
# date_part constant, and pg_query folds it to lower case. The rewriter
# gives it the caller's spelling back.
same SELECT id, EXTRACT(EPOCH FROM ts), EXTRACT(Epoch FROM ts) FROM t ORDER BY id
same SELECT id, EXTRACT(DOW FROM d), EXTRACT(Dow FROM d), EXTRACT(DOY FROM d), EXTRACT(DoY FROM d) FROM t ORDER BY id
same SELECT id, EXTRACT(ISODOW FROM d), EXTRACT(IsoDow FROM d) FROM t ORDER BY id
same SELECT id, EXTRACT(ISOYEAR FROM d), EXTRACT(IsoYear FROM d) FROM t ORDER BY id
same SELECT id, EXTRACT(JULIAN FROM d), EXTRACT(Julian FROM d) FROM t ORDER BY id
same SELECT id, EXTRACT(TIMEZONE_HOUR FROM tz), EXTRACT(Timezone_Minute FROM tz) FROM t ORDER BY id
same SELECT id FROM t WHERE EXTRACT(EPOCH FROM ts) > 1700000000 ORDER BY id
same SELECT id, EXTRACT(WEEK FROM d), EXTRACT(Quarter FROM d), EXTRACT(MilliSecond FROM ts) FROM t ORDER BY id
same SELECT id, EXTRACT("EPOCH" FROM ts), EXTRACT('Epoch' FROM ts) FROM t ORDER BY id

# The function forms.
same SELECT id, date_part('year', d), date_part('day', iv), datepart('month', d) FROM t ORDER BY id
same SELECT id, date_part('days', d), date_part('hours', ts) FROM t ORDER BY id
same SELECT id, EXTRACT('days' FROM d), EXTRACT('year' FROM d) FROM t ORDER BY id
same SELECT id, year(d), month(d), day(d), hour(ts), dayofweek(d) FROM t ORDER BY id
same SELECT id, date_trunc('month', d), date_trunc('hour', ts), date_trunc('year', tz) FROM t ORDER BY id
same SELECT id, date_diff('day', d, DATE '2026-01-01'), datediff('month', d, DATE '2026-01-01') FROM t ORDER BY id
same SELECT id, strftime(d, '%Y/%m'), strftime(ts, '%H:%M') FROM t ORDER BY id
same SELECT id, last_day(d), dayname(d), monthname(d) FROM t ORDER BY id
same SELECT id, make_date(2024, a + 1, 1) FROM t WHERE a IS NOT NULL ORDER BY id

# Plural units: DuckDB folds them only in the keyword form, so the trees
# differ (a pinned false refusal). Hint: the singular unit.
ERR_UNSUPPORTED_SYNTAX SELECT id, EXTRACT(days FROM d) FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, EXTRACT(hours FROM ts) FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, EXTRACT(years FROM d) FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, EXTRACT(seconds FROM ts) FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, EXTRACT(milliseconds FROM ts) FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, EXTRACT(microseconds FROM ts) FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, EXTRACT(minutes FROM ts) FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, EXTRACT(months FROM d) FROM t ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id FROM t WHERE EXTRACT(years FROM d) = 2025 ORDER BY id
ERR_UNSUPPORTED_SYNTAX SELECT id, CASE WHEN EXTRACT(days FROM d) > 15 THEN 1 END FROM t ORDER BY id

# SUBSTRING, TRIM, POSITION, OVERLAY.
same SELECT id, SUBSTRING(s FROM 2 FOR 2), SUBSTRING(s FROM 2), SUBSTRING(s, 2, 1) FROM t ORDER BY id
same SELECT id, substring(s, 1, 3), substr(s, 2) FROM t ORDER BY id
same SELECT id FROM t WHERE SUBSTRING(s FROM 1 FOR 1) = 'A' ORDER BY id
same SELECT id, TRIM(BOTH 'x' FROM s), TRIM(LEADING 'b' FROM s), TRIM(TRAILING 'b' FROM s) FROM t ORDER BY id
same SELECT id, TRIM(s), TRIM(FROM s), ltrim(s, 'a'), rtrim(s) FROM t ORDER BY id
same SELECT id, POSITION('b' IN s), strpos(s, 'b'), instr(s, 'b') FROM t ORDER BY id
same SELECT id FROM t WHERE POSITION('_' IN s) > 0 ORDER BY id
same SELECT id, CASE WHEN POSITION('!' IN s) > 0 THEN SUBSTRING(s FROM POSITION('!' IN s)) END FROM t ORDER BY id
ACL_REJECTED SELECT id, OVERLAY(s PLACING 'zz' FROM 2 FOR 1) FROM t

# Special date and time keywords.
same SELECT id, d < CURRENT_DATE FROM t ORDER BY id
same SELECT id FROM t WHERE d < current_date - 10000 ORDER BY id
same SELECT id, CURRENT_DATE - d > 0 FROM t ORDER BY id
