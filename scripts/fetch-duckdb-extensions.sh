#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Downloads DuckDB loadable extensions for a given platform into a
# directory laid out the way DuckDB's extension_directory expects:
#
#   <outdir>/<duckdb_version>/<platform>/<name>.duckdb_extension
#
# Used to pre-seed container images with the extensions that cannot be
# compiled into the binary (postgres_scanner, mysql_scanner, motherduck
# — see scripts/build-libduckdb.sh), so containers never download from
# extensions.duckdb.org at runtime. Drivers pick the files up via
# common.EnsureExtension, which LOADs from the extension directory
# before falling back to INSTALL; DuckDB verifies extension signatures
# at LOAD time, so pre-seeded files get the same integrity check as
# INSTALLed ones.
#
# Usage: fetch-duckdb-extensions.sh <platform> <outdir> <extension>...
#   e.g. fetch-duckdb-extensions.sh linux_amd64 third_party/duckdb-extensions/amd64 \
#          postgres_scanner mysql_scanner motherduck
#
# The DuckDB version comes from scripts/DUCKDB_VERSION.

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"

if [[ $# -lt 3 ]]; then
  echo "usage: $0 <platform> <outdir> <extension>..." >&2
  exit 1
fi

platform="$1"
outdir="$2"
shift 2

duckdb_version="$(tr -d '[:space:]' < "$repo_root/scripts/DUCKDB_VERSION")"
dest="$outdir/$duckdb_version/$platform"
mkdir -p "$dest"

for ext in "$@"; do
  if [[ -f "$dest/$ext.duckdb_extension" ]]; then
    echo "fetch-duckdb-extensions: $ext ($platform) already present, skipping"
    continue
  fi
  url="https://extensions.duckdb.org/$duckdb_version/$platform/$ext.duckdb_extension.gz"
  echo "fetch-duckdb-extensions: $url"
  curl -fsSL "$url" | gunzip > "$dest/$ext.duckdb_extension"
done

echo "fetch-duckdb-extensions: done -> $dest"
