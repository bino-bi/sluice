curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_intern.supersecret" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT o.id, o.amount_cents, c.name AS country, c.region FROM shop.main.orders o JOIN ref.main.countries c ON c.code = o.country_code ORDER BY o.id"}'
