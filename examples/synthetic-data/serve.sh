#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database, builds the synthetic snapshot and starts Sluice.
# REST listens on http://localhost:8080, the admin API on http://localhost:9091.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="synthetic-data-demo-pepper"
export SLUICE_APIKEY_DANA_HASH="d17f54c34e537bbf8231b8a23f27e4662c7ce344695ab33419c17cbdaf7c9371"
export SHOP_SYNTH_SEED="synthetic-data-demo-seed-0001"

# --8<-- [start:build]
sluice synth build shopsynth --config server.yaml
sluice synth build shopnoisy --config server.yaml
# --8<-- [end:build]

exec sluice serve --config server.yaml
