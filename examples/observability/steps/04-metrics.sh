curl -s http://localhost:9091/metrics \
  -H "Authorization: Bearer demo-admin-token-change-me" | grep "^sluice_audit"
