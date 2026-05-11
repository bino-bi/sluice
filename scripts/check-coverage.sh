#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Per-package coverage gate. Reads coverage.out (produced by
# `go test -coverprofile=coverage.out ./...`) and compares each package's
# statement coverage against the thresholds from plan/24-testing.md §11.
#
# Plain-bash (3.x) compatible — no associative arrays.
#
# Usage: scripts/check-coverage.sh [coverage.out]
# Exits 0 when every policed package meets its threshold, 1 otherwise.

set -euo pipefail

profile="${1:-coverage.out}"
if [[ ! -f "$profile" ]]; then
  echo "check-coverage: profile $profile not found" >&2
  echo "hint: make coverage" >&2
  exit 1
fi

# Thresholds as <package-suffix>:<percent>. The suffix is matched as a
# trailing path segment so a single entry like "pkg/errors" covers any
# module path that ends with it.
thresholds=(
  "internal/policy:80"
  "internal/rewriter:80"
  "internal/parser:75"
  "internal/pgquery:75"
  "internal/audit:85"
  "internal/identity:80"
  "pkg/apitypes:90"
  "pkg/errors:90"
  "pkg/mask:90"
  "pkg/datasource:90"
)
fallback_threshold=70

# Collapse per-file coverage into per-package averages.
summary="$(go tool cover -func="$profile" \
  | awk '
      /^total:/ { next }
      {
        file=$1; pct=$NF; sub(/%$/, "", pct); sub(/\/[^\/]*$/, "", file);
        sum[file]+=pct; cnt[file]+=1;
      }
      END {
        for (k in sum) { printf "%s\t%.1f\n", k, sum[k]/cnt[k]; }
      }
    ' | sort)"

violations=0
policed=0

echo "check-coverage: per-package summary"
printf "  %-70s %7s  %s\n" "package" "cover" "threshold"

while IFS=$'\t' read -r pkg pct; do
  [[ -z "$pkg" ]] && continue
  policed=$((policed + 1))
  threshold="$fallback_threshold"
  for entry in "${thresholds[@]}"; do
    suffix="${entry%%:*}"
    want="${entry##*:}"
    case "$pkg" in
      */"$suffix"|*/"$suffix"/*)
        threshold="$want"
        ;;
    esac
  done
  if awk -v a="$pct" -v b="$threshold" 'BEGIN{ exit !(a + 0 < b + 0) }'; then
    printf "  %-70s %7s  %s%% [FAIL]\n" "$pkg" "${pct}%" "$threshold"
    violations=$((violations + 1))
  else
    printf "  %-70s %7s  %s%%\n" "$pkg" "${pct}%" "$threshold"
  fi
done <<< "$summary"

echo
if [[ $violations -gt 0 ]]; then
  echo "check-coverage: $violations of $policed package(s) below threshold"
  exit 1
fi

echo "check-coverage: OK ($policed package(s) at or above threshold)"
