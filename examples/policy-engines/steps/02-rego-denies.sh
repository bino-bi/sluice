curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_ana.supersecret" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT c.name, p.amount_cents FROM shop.main.customers c JOIN shop.main.payments p ON p.customer_id = c.id"}'
