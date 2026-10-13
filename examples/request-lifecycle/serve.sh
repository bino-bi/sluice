#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="request-lifecycle-demo-pepper"
export SLUICE_AUDIT_GENESIS="request-lifecycle-demo-genesis"
export SLUICE_APIKEY_ANA_HASH="b77da2b454253e4ba3eba96d7bfae0a6ca14ef3b46837484d93f5fa2b32e1ebc"

exec sluice serve --config server.yaml
