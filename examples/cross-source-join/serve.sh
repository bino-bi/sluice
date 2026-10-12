#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates both sample databases and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db
go run ../../scripts/seed-duckdb.go seed-ref.sql data/ref.duckdb

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="cross-source-demo-pepper"
export SLUICE_AUDIT_GENESIS="cross-source-demo-genesis"
export SLUICE_APIKEY_ANALYST_HASH="6b76e522f99f8d61cb09bbd1780dc5055b96a3071d9996091c6c8618a8abaef8"
export SLUICE_APIKEY_INTERN_HASH="c5e746be30bdd8c9ba72f13ac5e2eb54847fcb759cecbef94e153e3769a9f44a"

exec sluice serve --config server.yaml
