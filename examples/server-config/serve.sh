#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and a secret file, then starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Only the owner can write the pepper file.
mkdir -p data/secrets
printf '%s' "server-config-demo-pepper" > data/secrets/apikey-pepper
chmod 600 data/secrets/apikey-pepper

# Demo secrets. Do not use these values in production.
# --8<-- [start:env]
export SLUICE_ADMIN__TOKEN="demo-admin-token-from-env"
export SLUICE_AUDIT_GENESIS="server-config-demo-genesis"
# server.yaml names a file under /etc/sluice. This example keeps the file in data/.
export SLUICE_IDENTITY__APIKEYPEPPER="secret://file/$PWD/data/secrets/apikey-pepper"
# --8<-- [end:env]
export SLUICE_APIKEY_ANALYST_HASH="7e171c9a7f3cbefc02ec788eb44279b496bee0e2851bf1085e86858a0fe61e62"

exec sluice serve --config server.yaml
