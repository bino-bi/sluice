curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_beta.supersecret" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT id, tenant_id, email FROM shop.main.customers ORDER BY id"}'
