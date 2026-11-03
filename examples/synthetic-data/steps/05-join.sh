curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_dana.world" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT c.city, count(*) AS orders FROM shopsynth.main.orders o LEFT JOIN shopsynth.main.customers c ON c.id = o.customer_id GROUP BY c.city ORDER BY c.city NULLS LAST"}'
