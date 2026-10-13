curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_analyst.supersecret" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT id, email, phone, ssn, birth_date FROM shop.main.customers ORDER BY id"}'
