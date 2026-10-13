#!/usr/bin/env bash
# SPDX-License-Identifier: CC-BY-4.0
# Creates the sample database and starts Sluice on http://localhost:8080.
# Sluice loads the Rego module from rego/ (policies.opa.moduleDir in server.yaml).
set -euo pipefail
cd "$(dirname "$0")"

python3 ../seed.py seed.sql data/shop.db

# Demo secrets. Do not use these values in production.
export SLUICE_APIKEY_PEPPER="opa-demo-pepper"
export SLUICE_AUDIT_GENESIS="opa-demo-genesis"
export SLUICE_APIKEY_ACME_HASH="6eba3b1cb2e4b52fdcdcc12ee07744b62cf98a2d195b0bdee80126411822a151"
export SLUICE_APIKEY_GUEST_HASH="3674263a90f21e3fcd58851d8b770d15d0b62130a265fab175503dd20386e4c3"

exec sluice serve --config server.yaml
