#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice.
# REST listens on http://localhost:8080, the admin API on http://localhost:9091.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Demo secret. Do not use this value in production.
export SLUICE_AUDIT_GENESIS="admin-api-demo-genesis"

exec sluice serve --config server.yaml
