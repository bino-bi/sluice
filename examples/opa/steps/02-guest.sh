curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_guest.supersecret" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT id, tenant_id, amount FROM shop.main.orders ORDER BY id"}'
