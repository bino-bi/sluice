curl -s http://localhost:8080/v1/query \
  -H "X-Api-Key: sl_demo_acme.supersecret" \
  -H "Content-Type: application/json" \
  -d @- <<'EOF'
{"sql": "SELECT id, tenant_id FROM shop.main.orders WHERE tenant_id = 'beta'"}
EOF
