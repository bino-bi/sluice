#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database, starts the OpenFGA stub on port 8091
# and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Stands in for an OpenFGA server. Answers from tuples.json.
python3 openfga-stub.py &

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="rebac-demo-pepper"
export SLUICE_AUDIT_GENESIS="rebac-demo-genesis"
export SLUICE_APIKEY_ALICE_HASH="388465e5f72443867410955367fc63d6f1ec34a08e7030d2286ef35b7c1079ea"
export SLUICE_APIKEY_BOB_HASH="6d419b5ff15df4dd98647ab7e6552600439c50ea49035775f7710e1dd1380aba"

exec sluice serve --config server.yaml
