#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db
# A step changes a policy, so the server reads a copy of policies.d.
cp -r policies.d data/policies

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="key-rotation-demo-pepper"
export SLUICE_AUDIT_GENESIS="key-rotation-demo-genesis"
export REPORT_BOT_2026A_HASH="1405a2fa32038a9a36404bf383c9973353d368b99ee31cd66afefa0b26c4e742"
export REPORT_BOT_2026B_HASH="358bc88e0b7dca0d0f207c0caa3463ca82cf3c7e79c1c4818a2666cdcf7ca980"

exec sluice serve --config server.yaml
