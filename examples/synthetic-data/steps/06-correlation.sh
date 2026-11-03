curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_dana.world" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT round(corr(amount, quantity), 1) AS correlation FROM shopsynth.main.orders"}'
