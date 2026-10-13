#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
# Sluice loads the YAML policies from policies.d/ and the Rego module from rego/.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="policy-engines-demo-pepper"
export SLUICE_AUDIT_GENESIS="policy-engines-demo-genesis"
export SLUICE_APIKEY_ANA_HASH="dfa3d98c19b0f6e018855645ea3fc2ca54e072c2b7a640e7832e947d3b273357"

exec sluice serve --config server.yaml
