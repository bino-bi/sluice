curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_hello.world" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT * FROM shop.main.admin_users"}'
