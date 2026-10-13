#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database, starts the webhook receiver on port 8090
# and starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Stands in for the approver's inbox. Stores each webhook under data/.
python3 webhook.py &

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="approval-workflow-demo-pepper"
export SLUICE_AUDIT_GENESIS="approval-workflow-demo-genesis"
export SLUICE_APIKEY_ALICE_HASH="6deff41913c878aeaa8890821dad4ad392757b1740380d17322872b2f89d904b"

exec sluice serve --config server.yaml
