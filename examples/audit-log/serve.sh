#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="audit-log-demo-pepper"
export SLUICE_AUDIT_GENESIS="audit-log-demo-genesis"
export SLUICE_APIKEY_ANALYST_HASH="4dfd511683fcc6da585b02d32441fc9d32ac38465c940ae17b6f22d01b5beb08"

exec sluice serve --config server.yaml
