#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="pii-masking-demo-pepper"
export SLUICE_AUDIT_GENESIS="pii-masking-demo-genesis"
export SLUICE_APIKEY_ANALYST_HASH="7b332af8ff84e684eb77932ce891de24f60a84f07c3ea0c78ff336f433f70d46"

exec sluice serve --config server.yaml
