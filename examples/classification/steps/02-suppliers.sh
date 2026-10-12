curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_alice.supersecret" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT name, email FROM shop.main.suppliers ORDER BY id"}'
