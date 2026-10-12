SELECT CAST(o.total AS float) AS total, o.meta::json AS meta, JSON '{"a":1}' AS doc, CAST(1 AS bit) AS flag FROM pg.public.orders o
