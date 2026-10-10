SELECT u.e FROM shop.main.customers c, unnest(ARRAY[c.email]) AS u(e)
