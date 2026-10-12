#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/hr.db

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="column-masks-demo-pepper"
export SLUICE_AUDIT_GENESIS="column-masks-demo-genesis"
export SLUICE_APIKEY_ANALYST_HASH="bb61a5854679d7d69802a15406261ea01df5b976d6acc7c683c847fcdd34b8fc"
# --8<-- [start:secrets]
# The fpe key must be 16, 24 or 32 bytes long.
export SLUICE_MASK_FPE_KEY="demo-fpe-key-16b"
export SLUICE_MASK_HMAC_KEY="demo-hmac-key"
export SLUICE_MASK_SEED="demo-seed"
# --8<-- [end:secrets]

exec sluice serve --config server.yaml
