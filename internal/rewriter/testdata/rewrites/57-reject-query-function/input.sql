SELECT t.username FROM (SELECT 1 FROM shop.main.customers LIMIT 1) x, query('SELECT username FROM shop.main.admin_users') t
