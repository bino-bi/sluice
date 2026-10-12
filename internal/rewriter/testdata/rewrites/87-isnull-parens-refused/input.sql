SELECT o.id FROM pg.public.orders o WHERE (o.status = 'open' OR o.total > 10) IS NULL
