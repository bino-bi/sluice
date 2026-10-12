#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="access-control-demo-pepper"
export SLUICE_AUDIT_GENESIS="access-control-demo-genesis"
export SLUICE_APIKEY_ANALYST_HASH="268d65863a9e12da66b9aa4d53bda6e587fdc2cab7d8f332df9b58cc91d38cca"
export SLUICE_APIKEY_ADMIN_HASH="bdc679ee924b23f4ce58d2c76b8d212bc83dd17a362b499a0f932fbb00161df7"

exec sluice serve --config server.yaml
