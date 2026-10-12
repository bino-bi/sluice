#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# The hash of the old key. A secret file needs an absolute path, so the example uses /tmp/sluice-key-rotation.
# Sluice refuses a secret file that group or others can write.
rm -rf /tmp/sluice-key-rotation && mkdir -p /tmp/sluice-key-rotation
echo "1405a2fa32038a9a36404bf383c9973353d368b99ee31cd66afefa0b26c4e742" > /tmp/sluice-key-rotation/report-bot-2026a.hash
chmod 600 /tmp/sluice-key-rotation/report-bot-2026a.hash

# The steps change the binding, so the server reads a copy of policies.d.
cp -r policies.d data/policies

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="key-rotation-demo-pepper"
export SLUICE_AUDIT_GENESIS="key-rotation-demo-genesis"

exec sluice serve --config server.yaml
