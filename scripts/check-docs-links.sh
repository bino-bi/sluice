#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# check-docs-links.sh — verify every relative link in docs/*.md resolves
# to an existing file. Bash 3 compatible (macOS default). Skips external
# (http[s]://) links and anchors-only links (#section).
#
# Exits 0 when clean, 1 when any link is broken.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DOCS="${ROOT}/docs"

if [ ! -d "${DOCS}" ]; then
  echo "docs/ not found at ${DOCS}" >&2
  exit 1
fi

broken=0
files_checked=0
links_checked=0

while IFS= read -r md; do
  files_checked=$((files_checked + 1))
  # Extract ](target) pairs, strip fragments, skip externals.
  # POSIX grep -o is available on macOS; we use Perl-style via sed.
  # shellcheck disable=SC2162
  while read -r target; do
    [ -z "${target}" ] && continue
    links_checked=$((links_checked + 1))

    case "${target}" in
      'http://'*|'https://'*|'mailto:'*|'#'*) continue ;;
    esac

    # Strip the anchor portion.
    path="${target%%#*}"
    [ -z "${path}" ] && continue

    # Resolve relative to the containing file.
    dir="$(dirname "${md}")"
    resolved="${dir}/${path}"

    if [ ! -e "${resolved}" ]; then
      echo "BROKEN  ${md}: -> ${target}"
      broken=$((broken + 1))
    fi
  done < <(sed -nE 's/.*\]\(([^)]+)\).*/\1/gp' "${md}" | tr ' ' '\n' || true)
done < <(find "${DOCS}" -type f -name '*.md')

echo "checked ${links_checked} links across ${files_checked} files"
if [ "${broken}" -gt 0 ]; then
  echo "${broken} broken link(s)"
  exit 1
fi
echo "all links resolve"
