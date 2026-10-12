#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="row-filters-demo-pepper"
export SLUICE_AUDIT_GENESIS="row-filters-demo-genesis"
export SLUICE_APIKEY_ANA_HASH="4157fb7842ac8ba39db2826c069a2e390b5e9386174f14663d807dbf2f16a183"
export SLUICE_APIKEY_BEN_HASH="65a9a122057f0d1dd6e85f92a2a82c9b1e72d999025c072ba9f28086579cf7d7"

exec sluice serve --config server.yaml
