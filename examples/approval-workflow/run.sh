#!/usr/bin/env bash
# Drives the approval workflow end-to-end against a local webhook catcher.
# Requires: a running `sluice serve` with this example's config, plus jq.
set -euo pipefail

BASE="${SLUICE_BASE:-http://localhost:8080}"
API_KEY="${SLUICE_API_KEY:-sl_demo_alice:world}"
SQL="SELECT ssn FROM shop.main.people WHERE country = 'de'"

echo "1) Submit a PII query — expect 202 ERR_APPROVAL_PENDING"
resp="$(curl -s -w '\n%{http_code}' -X POST "$BASE/v1/query" \
  -H "Authorization: ApiKey $API_KEY" -H 'Content-Type: application/json' \
  -d "{\"sql\": \"$SQL\"}")"
body="$(echo "$resp" | head -n1)"
echo "   $body"
approval_id="$(echo "$body" | jq -r '.details.approval_id // .approval_id // empty')"

echo "2) The webhook catcher should have recorded accept_url; paste it here or read from your catcher."
echo "   (In a real deployment the approver clicks the accept_url from the webhook.)"
read -r -p "   accept_url: " ACCEPT_URL

echo "3) Approver accepts"
curl -s "$ACCEPT_URL" ; echo

echo "4) Re-run the IDENTICAL query — expect rows"
curl -s -X POST "$BASE/v1/query" \
  -H "Authorization: ApiKey $API_KEY" -H 'Content-Type: application/json' \
  -d "{\"sql\": \"$SQL\"}" | jq .

echo "5) Re-run once more — expect ERR_APPROVAL_PENDING again (grant is single-use)"
curl -s -X POST "$BASE/v1/query" \
  -H "Authorization: ApiKey $API_KEY" -H 'Content-Type: application/json' \
  -d "{\"sql\": \"$SQL\"}" | jq -r '.code'
