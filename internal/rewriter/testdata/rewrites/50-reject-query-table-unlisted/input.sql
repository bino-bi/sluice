SELECT t.id, t.username FROM (SELECT 1 FROM shop.main.customers LIMIT 1) x, query_table('shop.main.admin_users') t
