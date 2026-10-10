SELECT s.name, s.value FROM (SELECT 1 FROM shop.main.customers LIMIT 1) x, duckdb_settings() s
