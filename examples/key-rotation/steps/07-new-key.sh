curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: report-bot-2026b.new-secret" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT id, name FROM shop.main.customers ORDER BY id"}'
