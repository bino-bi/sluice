#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# The hash of the old key. Sluice refuses a secret file that group or others can write.
mkdir -p data/secrets
echo "1405a2fa32038a9a36404bf383c9973353d368b99ee31cd66afefa0b26c4e742" > data/secrets/report-bot-2026a.hash
chmod 600 data/secrets/report-bot-2026a.hash

# The steps change the binding, so the server reads a copy of policies.d.
cp -r policies.d data/policies
# The bindings name files under /etc/sluice/secrets. The copies name data/secrets.
mkdir -p data/both data/rotated
secrets="$PWD/data/secrets"
sed "s|/etc/sluice/secrets|$secrets|" policies.d/binding-report-bot.yaml > data/policies/binding-report-bot.yaml
sed "s|/etc/sluice/secrets|$secrets|" both/binding-report-bot.yaml > data/both/binding-report-bot.yaml
sed "s|/etc/sluice/secrets|$secrets|" rotated/binding-report-bot.yaml > data/rotated/binding-report-bot.yaml

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="key-rotation-demo-pepper"
export SLUICE_AUDIT_GENESIS="key-rotation-demo-genesis"

exec sluice serve --config server.yaml
