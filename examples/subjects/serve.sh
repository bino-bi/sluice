#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="subjects-demo-pepper"
export SLUICE_AUDIT_GENESIS="subjects-demo-genesis"
export SLUICE_JWT_SECRET="subjects-demo-jwt-secret"
export SLUICE_APIKEY_REPORT_HASH="620a77cf17a34dd71c24dff75930f8b7333b80894faf65670e52d07bb63a57b1"
export SLUICE_APIKEY_AGENT_HASH="005be2c60e937a564fd97fa90a344cc65b60ea00ad8c796e57969727ef0dbc54"

exec sluice serve --config server.yaml
