curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_analyst.supersecret" \
  -H "Content-Type: application/json" \
  -d @- <<'EOF'
{"sql": "SELECT id FROM shop.main.customers WHERE email LIKE '%alice%'"}
EOF
