curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_intern.supersecret" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT code, name, region FROM ref.main.countries ORDER BY code"}'
