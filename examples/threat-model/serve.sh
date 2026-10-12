#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="threat-model-demo-pepper"
export SLUICE_AUDIT_GENESIS="threat-model-demo-genesis"
export SLUICE_APIKEY_AGENT_HASH="3b67cd1881c30e728ac139bbcf3c64fdf9efeb063c4faa5c96633fb1a4355dde"

exec sluice serve --config server.yaml
