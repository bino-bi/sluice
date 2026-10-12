#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="break-glass-demo-pepper"
export SLUICE_AUDIT_GENESIS="break-glass-demo-genesis"
export SLUICE_APIKEY_ANALYST_HASH="dced9bb29254120456acf83a91a38f5fe6109ca8f02c9a5f0547086956f797f0"
export SLUICE_APIKEY_SRE_HASH="6b20dbd5b12867e621979127a95f18ccaccc5e5ef5af042c07c2f611dd966b09"

exec sluice serve --config server.yaml
