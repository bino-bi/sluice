SELECT id FROM shop.main.orders WHERE customer_id IN (SELECT id FROM shop.main.customers) AND EXISTS (SELECT 1 FROM shop.main.customers c WHERE c.id = 3)
