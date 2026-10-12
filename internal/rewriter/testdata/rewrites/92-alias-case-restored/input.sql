WITH totals(OrderId, Total) AS (SELECT o.id, o.total FROM pg.public.orders o) SELECT t.OrderId, struct_pack(Total := t.Total) AS Info, to_json(t) AS Doc FROM totals t
