SELECT d.database_name, d.path FROM (SELECT 1 FROM shop.main.customers LIMIT 1) x, duckdb_databases() d
