curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_admin.supersecret" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT employee, amount_cents FROM shop.main.salaries"}'
