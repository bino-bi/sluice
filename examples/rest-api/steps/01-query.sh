curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_alice.world" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT id, name, price_cents FROM shop.main.products ORDER BY id", "max_rows": 2}'
