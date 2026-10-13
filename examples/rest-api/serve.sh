#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="rest-api-demo-pepper"
export SLUICE_AUDIT_GENESIS="rest-api-demo-genesis"
export SLUICE_APIKEY_ALICE_HASH="de82423b561286a0e063beb4d63a3924ef5b5100b40cb7175f896fa9ed81fce0"

exec sluice serve --config server.yaml
