SELECT c.tenant_id, count(*) AS c FROM shop.main.customers c GROUP BY c.tenant_id HAVING c > 0
