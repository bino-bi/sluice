#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="hello-sluice-demo-pepper"
export SLUICE_AUDIT_GENESIS="hello-sluice-demo-genesis"
export SLUICE_APIKEY_HELLO_HASH="c0c9ab61795cbbb79767ef51dd0199cfbfd93ed4fd5fe767af1e81192f385153"

exec sluice serve --config server.yaml
