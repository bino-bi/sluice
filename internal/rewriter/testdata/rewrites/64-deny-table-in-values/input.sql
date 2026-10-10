SELECT v.u FROM (VALUES ((SELECT string_agg(username, ',') FROM shop.main.admin_users))) v(u), (SELECT 1 FROM shop.main.customers LIMIT 1) x
