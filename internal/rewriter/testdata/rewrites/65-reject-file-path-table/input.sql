SELECT t.id, t.email FROM (SELECT 1 FROM shop.main.customers LIMIT 1) x, "s3://bucket/customers.parquet" t
