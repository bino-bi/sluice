curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_agent.supersecret" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT id, country, amount FROM shop.main.orders ORDER BY id"}'
