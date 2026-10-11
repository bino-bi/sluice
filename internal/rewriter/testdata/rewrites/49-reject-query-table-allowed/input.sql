SELECT t.id, t.email, t.tenant_id FROM (SELECT 1 FROM shop.main.customers LIMIT 1) x, query_table('shop.main.customers') t
