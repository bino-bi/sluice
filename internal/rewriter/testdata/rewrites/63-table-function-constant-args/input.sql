SELECT c.id, g.i FROM shop.main.customers c, generate_series(1, 2) AS g(i)
