#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="observability-demo-pepper"
export SLUICE_AUDIT_GENESIS="observability-demo-genesis"
export SLUICE_APIKEY_ANALYST_HASH="2778ab40c4ef776d4f03188568d1af39b4774b50a7d4d5dc99a5fc8cc1f234a1"

# Sluice writes its log to stderr. The steps read it from this file.
exec sluice serve --config server.yaml 2> data/sluice.log
