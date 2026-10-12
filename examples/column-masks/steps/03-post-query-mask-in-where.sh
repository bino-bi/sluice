curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_analyst.supersecret" \
  -H "Content-Type: application/json" \
  -d @- <<'JSON'
{"sql": "SELECT id FROM hr.main.employees WHERE card LIKE '4111%'"}
JSON
