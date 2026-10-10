SELECT t.id, t.username FROM (SELECT 1 FROM shop.main.customers LIMIT 1) x, sqlite_scan('/data/shop.db', 'admin_users') t
