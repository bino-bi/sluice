curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_dana.world" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT product, count(*) AS orders FROM shopnoisy.main.orders GROUP BY product ORDER BY product"}'
