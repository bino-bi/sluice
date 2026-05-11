#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Verifies that every .go file in the repo carries the correct SPDX header:
#   - pkg/** and sdk/**         → Apache-2.0
#   - everything else (except vendor, testdata, and internal planning dirs) → AGPL-3.0-or-later
#
# Exits 0 on success, 1 on any violation.

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

apache='// SPDX-License-Identifier: Apache-2.0'
agpl='// SPDX-License-Identifier: AGPL-3.0-or-later'

violations=0
checked=0

is_excluded() {
  local p="$1"
  case "$p" in
    ./__INTERNAL/*|./vendor/*|./testdata/*|*/testdata/*) return 0 ;;
    *) return 1 ;;
  esac
}

while IFS= read -r -d '' f; do
  if is_excluded "$f"; then
    continue
  fi

  checked=$((checked + 1))
  first="$(head -n 1 "$f" || true)"

  # pkg/** and sdk/** → Apache-2.0
  if [[ "$f" == ./pkg/* || "$f" == ./sdk/* ]]; then
    if [[ "$first" != "$apache" ]]; then
      echo "SPDX violation: $f must start with: $apache"
      violations=$((violations + 1))
    fi
  else
    if [[ "$first" != "$agpl" ]]; then
      echo "SPDX violation: $f must start with: $agpl"
      violations=$((violations + 1))
    fi
  fi
done < <(find . -type f -name '*.go' -print0)

if [[ $violations -gt 0 ]]; then
  echo
  echo "check-spdx: $violations file(s) missing or wrong header ($checked scanned)"
  exit 1
fi

echo "check-spdx: OK ($checked .go files scanned)"
