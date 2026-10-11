WITH recent AS (SELECT customer_id FROM shop.main.orders) SELECT * FROM recent r JOIN shop.main.customers c ON c.id = r.customer_id, generate_series(1, 2), (SELECT 1 AS one)
