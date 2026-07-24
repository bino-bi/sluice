SELECT id FROM pg.public.orders JOIN pg.public.customers USING (customer_id)
