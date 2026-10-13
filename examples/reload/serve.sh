#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# The steps change policy files. They work on a copy, not on the files in git.
cp -r policies.d data/policies

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="reload-demo-pepper"
export SLUICE_AUDIT_GENESIS="reload-demo-genesis"
export SLUICE_APIKEY_ANALYST_HASH="2644e5ba3d9883e11b98712472a49e80f8b702978c63a4eac2d37e51b0d2b50b"

exec sluice serve --config server.yaml
