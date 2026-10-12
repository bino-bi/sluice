curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_acme.supersecret" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT (SELECT count(*) FROM shop.main.orders) AS orders_visible"}'
