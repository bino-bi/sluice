curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_analyst.supersecret" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT o.id, c.email FROM shop.main.orders o JOIN shop.main.customers c ON c.name = o.customer"}'
