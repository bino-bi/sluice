SELECT t.id FROM (SELECT 1 FROM shop.main.customers LIMIT 1) x, read_parquet('s3://bucket/data.parquet') t
