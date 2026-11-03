#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Builds a DuckDB static bundle library (libduckdb_bundle.a) with the
# open-source extensions Sluice's datasource drivers need compiled in:
#
#   httpfs excel sqlite_scanner
#
# plus the defaults the prebuilt go-duckdb bindings ship (json, icu,
# autocomplete, parquet, core_functions and the httplib and
# loadable_extensions capabilities). Only the tpch and tpcds benchmark
# extensions of the prebuilt libraries are left out.
#
# The database-scanner extensions cannot be bundled — their drivers fall
# back to INSTALL/LOAD at first attach:
#   - mysql_scanner: duckdb-mysql's CMake only defines a loadable
#     extension target (no build_static_extension).
#   - postgres_scanner: its vendored libpq defines the same PostgreSQL
#     symbols (pg_wchar_table, pg_enc2name_tbl, ...) as pg_query_go,
#     which Sluice links for SQL parsing — duplicate-symbol collision.
# Both are DONT_LINK upstream for exactly these reasons.
#
# The bundle is linked into the sluice binary via go-duckdb's supported
# `duckdb_use_static_lib` build tag (see `make build-static`), so those
# extensions never have to be downloaded from extensions.duckdb.org at
# runtime. motherduck is closed-source and stays runtime-installed.
#
# Extension third-party dependencies (OpenSSL for httpfs, minizip-ng
# for excel, ...) come from vcpkg — DuckDB's own `make setup-vcpkg`
# pins the matching vcpkg revision. DuckDB 2.0 dropped the
# `bundle-library` target and registers static extensions through a
# generated loader (duckdb_register_static_extensions, which the Go
# bindings call). This script compiles that loader and merges it with
# the DuckDB, extension and vcpkg archives, so the output is still a
# single self-contained .a linked with just `-lduckdb_bundle` plus the
# platform C++ runtime.
#
# Output: third_party/libduckdb/<goos>_<goarch>/libduckdb_bundle.a
#
# The build is idempotent: an existing libduckdb_bundle.a of the same
# DuckDB version (the VERSION file next to it) is reused.
# Set LIBDUCKDB_FORCE=1 to rebuild, LIBDUCKDB_WORKDIR to relocate the
# DuckDB source checkout (CI puts it outside the cache path).
#
# First build takes ~20-40 minutes (full DuckDB C++ release build plus
# vcpkg-built dependencies); requires git, cmake, ninja, python3, a C++
# toolchain, and vcpkg's bootstrap needs curl, zip, unzip, tar, pkg-config.

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

duckdb_version="$(tr -d '[:space:]' < scripts/DUCKDB_VERSION)"

# Guard against scripts/DUCKDB_VERSION drifting from the DuckDB release
# bundled by go.mod: duckdb-go encodes it as v2.(MAJOR*10000+MINOR*100+PATCH).x.
mod="$(go list -m -f '{{.Version}}' github.com/duckdb/duckdb-go/v2)"
enc="${mod#v2.}"
enc="${enc%%.*}"
expect="v$((enc / 10000)).$(((enc / 100) % 100)).$((enc % 100))"
if [[ "$expect" != "$duckdb_version" ]]; then
  echo "build-libduckdb: scripts/DUCKDB_VERSION ($duckdb_version) out of sync with go.mod ($mod bundles DuckDB $expect)" >&2
  exit 1
fi

goos="$(go env GOOS)"
goarch="$(go env GOARCH)"
out_dir="$repo_root/third_party/libduckdb/${goos}_${goarch}"
workdir="${LIBDUCKDB_WORKDIR:-$repo_root/third_party/libduckdb/src}"

if [[ -f "$out_dir/libduckdb_bundle.a" && "$(cat "$out_dir/VERSION" 2>/dev/null)" == "$duckdb_version" &&
  "${LIBDUCKDB_FORCE:-0}" != "1" ]]; then
  echo "build-libduckdb: $out_dir/libduckdb_bundle.a exists, nothing to do (LIBDUCKDB_FORCE=1 to rebuild)"
  exit 0
fi

if [[ -d "$workdir/.git" ]]; then
  current_tag="$(git -C "$workdir" describe --tags --exact-match 2>/dev/null || echo none)"
  if [[ "$current_tag" != "$duckdb_version" ]]; then
    echo "build-libduckdb: existing checkout at $workdir is $current_tag, refetching $duckdb_version"
    git -C "$workdir" fetch --depth 1 origin "refs/tags/$duckdb_version:refs/tags/$duckdb_version"
    git -C "$workdir" checkout -f "$duckdb_version"
    git -C "$workdir" clean -fdx
  fi
else
  git clone --depth 1 --branch "$duckdb_version" https://github.com/duckdb/duckdb.git "$workdir"
fi

echo "build-libduckdb: building DuckDB $duckdb_version bundle for ${goos}/${goarch} (this can take 20-40 min cold)"

# Xcode clang 17+ promotes its new default-const-init warnings to errors
# under the -Werror the libmariadb port compiles with; suppress them via
# an overlay triplet (harmless where the flag is unknown, since no other
# diagnostic fires in a passing build).
if [[ "$goos" == "darwin" ]]; then
  overlay_dir="$repo_root/third_party/libduckdb/overlay-triplets"
  mkdir -p "$overlay_dir"
  cat > "$overlay_dir/arm64-osx.cmake" <<'EOF'
set(VCPKG_TARGET_ARCHITECTURE arm64)
set(VCPKG_CRT_LINKAGE dynamic)
set(VCPKG_LIBRARY_LINKAGE static)
set(VCPKG_CMAKE_SYSTEM_NAME Darwin)
set(VCPKG_OSX_ARCHITECTURES arm64)
set(VCPKG_C_FLAGS "-Wno-default-const-init-var-unsafe")
set(VCPKG_CXX_FLAGS "-Wno-default-const-init-var-unsafe")
EOF
  export VCPKG_OVERLAY_TRIPLETS="$overlay_dir"
fi

(
  cd "$workdir"
  # Clones + bootstraps the vcpkg revision this DuckDB release pins.
  make setup-vcpkg
  # DuckDB 2.0 takes the release version from the environment, not from
  # git; without it the build is a dev build and looks for extensions
  # under the commit hash instead of the version.
  DUCKDB_VERSION="$duckdb_version" \
  CORE_EXTENSIONS='json;icu;autocomplete;httpfs;excel;sqlite_scanner' \
  EXTENSION_STATIC_BUILD=1 \
  DUCKDB_CAPABILITIES='httplib;loadable_extensions' \
  USE_MERGED_VCPKG_MANIFEST=1 \
  VCPKG_TOOLCHAIN_PATH="$workdir/vcpkg/scripts/buildsystems/vcpkg.cmake" \
  DISABLE_SHELL=1 \
  GEN=ninja \
  make release
  # Registers every capability and extension archive the build left.
  make static_extension_loader

  cd build/release
  rm -rf bundle libduckdb_bundle.a
  mkdir -p bundle/loader
  "${CC:-cc}" -O2 -fPIC -I "$workdir/src/include" -c static_extension_loader.c -o bundle/loader/static_extension_loader.o
  # One directory per archive, so members of the same name do not
  # overwrite each other. vcpkg's debug builds are left out.
  {
    ls src/libduckdb_static.a src/main/http/libduckdb_httplib.a \
      src/main/extension/libduckdb_loadable_extensions.a
    ls third_party/*/libduckdb_*.a extension/*/lib*_extension.a 2>/dev/null || true
    find vcpkg_installed -name '*.a' -not -path '*/debug/*' 2>/dev/null || true
  } | while read -r lib; do
    dir="bundle/$(echo "$lib" | tr '/' '_')"
    mkdir -p "$dir"
    (cd "$dir" && "${AR:-ar}" -x "../../$lib")
  done
  # q appends: xargs may split the list, and r would replace a member of
  # the same name from an earlier call.
  find bundle -name '*.o' -print0 | xargs -0 "${AR:-ar}" qc libduckdb_bundle.a
  "${RANLIB:-ranlib}" libduckdb_bundle.a
)

mkdir -p "$out_dir"
cp "$workdir/build/release/libduckdb_bundle.a" "$out_dir/"
echo "$duckdb_version" > "$out_dir/VERSION"

echo "build-libduckdb: done -> $out_dir/libduckdb_bundle.a"
