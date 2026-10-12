curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_ana.supersecret" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT id, amount_cents // 100 * 100 AS whole_euros FROM shop.main.orders ORDER BY id"}'
