curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_alice.world" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT name, ssn FROM shop.main.people ORDER BY id"}'
