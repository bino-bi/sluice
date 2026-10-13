#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="hardening-demo-pepper"
export SLUICE_AUDIT_GENESIS="hardening-demo-genesis"
export SLUICE_APIKEY_REPORT_BOT_HASH="3fb0c18ceed722c7e96bc05c29ce866875ae046ac9df124fabc1a8d8431383e7"

exec sluice serve --config server.yaml
