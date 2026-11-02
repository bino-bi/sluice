curl -s http://localhost:8080/v1/sample \
  -H "X-Api-Key: sl_demo_alice.world" \
  -H "Content-Type: application/json" \
  -d '{"table": "shop.main.products", "rows": 2, "seed": 7}'
