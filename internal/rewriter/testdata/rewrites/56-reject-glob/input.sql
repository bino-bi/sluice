SELECT t.file FROM (SELECT 1 FROM shop.main.customers LIMIT 1) x, glob('/data/*') t
