#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice.
# REST listens on http://localhost:8080, MCP on http://localhost:8081.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="mcp-tools-demo-pepper"
export SLUICE_AUDIT_GENESIS="mcp-tools-demo-genesis"
export SLUICE_APIKEY_MCP_HASH="3b7af6466ce95fa8e8fed82f3dcdc456bd9892fdb75ef9c3045eda27a5a6fcbc"

exec sluice serve --config server.yaml
