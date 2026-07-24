WITH RECURSIVE x AS (SELECT id, parent_id FROM pg.public.orders UNION ALL SELECT o.id, o.parent_id FROM pg.public.orders o JOIN x ON o.parent_id = x.id) SELECT id FROM x
