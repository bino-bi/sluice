SELECT o.id, o.created_at + INTERVAL '1' WEEK FROM pg.public.orders o
