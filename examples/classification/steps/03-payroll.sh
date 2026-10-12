curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_alice.supersecret" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT name, salary_eur FROM shop.main.payroll"}'
