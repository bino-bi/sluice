SELECT v.n FROM (VALUES ((SELECT count(*) FROM shop.main.customers))) v(n)
