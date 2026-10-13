#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="classification-demo-pepper"
export SLUICE_AUDIT_GENESIS="classification-demo-genesis"
export SLUICE_APIKEY_ALICE_HASH="ab57cb07ec2e885124f9bde54df88257615fef20019f4ed140c6ff7756186711"

exec sluice serve --config server.yaml
