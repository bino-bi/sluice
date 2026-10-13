curl -s http://localhost:8080/v1/query \
  -H "Authorization: Bearer $(python3 mint-jwt.py token-claims.json)" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT id, country, amount FROM shop.main.orders ORDER BY id"}'
