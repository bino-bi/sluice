#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# check-generated-docs.sh — fail when `make docs-generate` would modify
# any committed reference page. Wired into docs-deploy.yaml so a PR that
# adds a pkg/errors.Code must also regenerate error-codes.md.
#
# Implementation: delegate to `make docs-check`, which runs every
# scripts/gen-*-docs.go with -check.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "${ROOT}"

make docs-check
