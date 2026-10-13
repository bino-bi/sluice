#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and a certificate, then starts Sluice on http://localhost:8080.
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# --8<-- [start:cert]
# A self-signed certificate for localhost. In production, use one from your CA.
mkdir -p data/tls
openssl req -x509 -newkey rsa:2048 -nodes -days 30 \
  -subj "/CN=localhost" -addext "subjectAltName=DNS:localhost" \
  -keyout data/tls/server.key -out data/tls/server.crt 2>/dev/null
# --8<-- [end:cert]

# --8<-- [start:start]
# Demo secrets. Do not use these values in production.
export SLUICE_ADMIN__TOKEN="demo-admin-token-change-me"
export SLUICE_AUDIT_GENESIS="deployment-demo-genesis"

exec sluice serve --config server.yaml
# --8<-- [end:start]
