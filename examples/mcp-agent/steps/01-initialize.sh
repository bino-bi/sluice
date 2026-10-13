curl -s http://localhost:8081/ \
  -H "X-Api-Key: mcp.supersecret" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {
        "protocolVersion": "2025-06-18",
        "capabilities": {},
        "clientInfo": {"name": "curl", "version": "1.0"}}}' \
  -o /dev/null -w '%header{mcp-session-id}' > data/mcp-session
