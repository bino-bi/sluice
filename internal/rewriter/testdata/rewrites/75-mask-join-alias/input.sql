SELECT j.email FROM (shop.main.customers c JOIN shop.main.orders o ON o.customer_id = c.id) AS j
