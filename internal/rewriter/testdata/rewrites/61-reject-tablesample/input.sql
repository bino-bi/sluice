SELECT id, email, tenant_id FROM shop.main.customers TABLESAMPLE reservoir(1000)
