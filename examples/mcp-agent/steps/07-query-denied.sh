curl -s http://localhost:8081/ \
  -H "X-Api-Key: mcp.supersecret" \
  -H "Mcp-Session-Id: $(cat data/mcp-session)" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": {
        "name": "execute_sql",
        "arguments": {"sql": "SELECT name, email FROM shop.main.customers"}}}'
