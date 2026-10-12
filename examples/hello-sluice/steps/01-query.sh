curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_hello.world" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT id, email, tenant_id FROM shop.main.customers ORDER BY id"}'
