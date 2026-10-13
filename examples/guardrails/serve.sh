#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="guardrails-demo-pepper"
export SLUICE_AUDIT_GENESIS="guardrails-demo-genesis"
export SLUICE_APIKEY_AGENT_HASH="4d888d2a1dd133cac730ced012238edb8d1c2861af3b4fbf9304ae905627dfec"

exec sluice serve --config server.yaml
