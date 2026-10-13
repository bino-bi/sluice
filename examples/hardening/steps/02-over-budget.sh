curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_report_bot.supersecret" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT id, name FROM shop.main.customers ORDER BY id"}'
