#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="multi-tenant-demo-pepper"
export SLUICE_AUDIT_GENESIS="multi-tenant-demo-genesis"
export SLUICE_APIKEY_ACME_HASH="5b42d0c29c4c20b5e7b0d67392a0aafbb18ac33355cdd2d432738230e09365ea"
export SLUICE_APIKEY_BETA_HASH="04fdc1c07ea3005824b81762e4d5e608a4d10a5a96db9003b6c0b68090b6b04a"

exec sluice serve --config server.yaml
