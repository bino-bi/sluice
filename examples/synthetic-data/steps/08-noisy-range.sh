curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_dana.world" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT round(min(amount)) AS min_amount, round(max(amount)) AS max_amount FROM shopnoisy.main.orders"}'
