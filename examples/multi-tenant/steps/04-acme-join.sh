curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_acme.supersecret" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT c.email, o.id, o.amount_cents FROM shop.main.customers c JOIN shop.main.orders o ON o.customer_id = c.id ORDER BY o.id"}'
