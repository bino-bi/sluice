#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice.
# REST listens on http://localhost:8080, MCP on http://localhost:8081.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="mcp-agent-demo-pepper"
export SLUICE_AUDIT_GENESIS="mcp-agent-demo-genesis"
export SLUICE_APIKEY_MCP_HASH="341081ca3a9ec503c1ee5ab902f514de90c9978a103d67ee43ed836247da40bb"

exec sluice serve --config server.yaml
