#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Per-package coverage gate. Reads coverage.out (produced by
# `go test -coverprofile=coverage.out ./...`) and compares each package's
# statement coverage against an enforced floor.
#
# Two numbers per package matter here:
#
#   floor  what CI enforces today — the measured value at the time the
#          baseline was taken, minus ~1pp of run-to-run slack.
#   goal   the target from plan/24-testing.md §11.
#
# Where floor < goal the difference is coverage debt: the gate holds the
# line so it cannot regress, and prints a ratchet hint once a package has
# climbed 5pp clear of its floor so the floor gets raised toward the goal
# instead of drifting into permanent slack.
#
# Plain-bash (3.x) compatible — no associative arrays.
#
# Usage: scripts/check-coverage.sh [coverage.out]
# Exits 0 when every policed package meets its floor, 1 otherwise.

set -euo pipefail

profile="${1:-coverage.out}"
if [[ ! -f "$profile" ]]; then
  echo "check-coverage: profile $profile not found" >&2
  echo "hint: make coverage" >&2
  exit 1
fi

# <package-suffix>:<floor>:<goal>, matched as an exact trailing path
# segment. Entries are only needed where floor differs from the default,
# or where §11 sets a goal above it.
thresholds=(
  # At goal — held there.
  "internal/parser:75:75"
  "internal/pgquery:75:75"
  "internal/identity:80:80"
  "pkg/datasource:90:90"
  "pkg/errors:90:90"

  # Coverage debt: floor is where we are, goal is where §11 wants us.
  "cmd/sluice:45:70"
  "internal/audit:78:85"
  "internal/datasource/drivers/common:58:70"
  "internal/datasource/drivers/duckdbfile:22:70"
  "internal/datasource/drivers/httpfile:40:70"
  "internal/datasource/drivers/motherduck:20:70"
  "internal/datasource/drivers/mysql:38:70"
  "internal/datasource/drivers/postgres:37:70"
  "internal/datasource/drivers/s3:39:70"
  "internal/datasource/drivers/sqlitefile:30:70"
  "internal/opaengine:63:70"
  "internal/parserbackend:32:70"
  "internal/policy:67:80"
  "internal/policytest:68:70"
  "internal/rebac:57:70"
  "internal/rewriter:65:80"
  "internal/transport/admin:62:70"
  "internal/transport/mcp:27:70"
  "internal/version:56:70"
  "pkg/apitypes:58:90"
  "pkg/datasource/testfakes:89:90"
  "pkg/mask:78:90"
)
fallback_threshold=70

# Packages with no meaningful coverage of their own. tlstest exists purely
# to hand TLS fixtures to other packages' tests; it has no test files, so
# it reports 0% by construction.
exclusions=(
  "internal/transport/tlsutil/tlstest"
)

# Statement-weighted coverage per package, computed from the profile
# blocks directly. `go tool cover -func` reports per-function percentages;
# averaging those weights a one-line helper the same as a 200-line
# function and misreports badly (drivers/duckdbfile reads 66.9% that way
# against a true 23.1%).
#
# Block lines are `<import-path>/<file>.go:<range> <numStmt> <count>`. The
# same block can appear more than once when several test binaries cover
# it, so dedupe on the block key and keep the highest count.
summary="$(awk '
    /^mode:/ { next }
    {
      key = $1;
      stmt[key] = $2;
      if (!(key in seen) || $3 > seen[key]) seen[key] = $3;
    }
    END {
      for (k in seen) {
        split(k, parts, ":");
        pkg = parts[1];
        sub(/\/[^\/]*$/, "", pkg);
        total[pkg] += stmt[k];
        if (seen[k] > 0) covered[pkg] += stmt[k];
      }
      for (p in total) {
        if (total[p] > 0) printf "%s\t%.1f\n", p, 100 * covered[p] / total[p];
      }
    }
  ' "$profile" | sort)"

if [[ -z "$summary" ]]; then
  echo "check-coverage: $profile contained no coverage blocks" >&2
  exit 1
fi

violations=0
policed=0
skipped=0
hints=()

echo "check-coverage: per-package summary"
printf "  %-70s %7s  %s\n" "package" "cover" "floor"

while IFS=$'\t' read -r pkg pct; do
  [[ -z "$pkg" ]] && continue

  excluded=0
  for entry in "${exclusions[@]}"; do
    case "$pkg" in
      */"$entry") excluded=1 ;;
    esac
  done
  if [[ $excluded -eq 1 ]]; then
    printf "  %-70s %7s  %s\n" "$pkg" "${pct}%" "— excluded"
    skipped=$((skipped + 1))
    continue
  fi

  policed=$((policed + 1))
  floor="$fallback_threshold"
  goal="$fallback_threshold"
  for entry in "${thresholds[@]}"; do
    suffix="${entry%%:*}"
    rest="${entry#*:}"
    case "$pkg" in
      */"$suffix")
        floor="${rest%%:*}"
        goal="${rest##*:}"
        ;;
    esac
  done

  if awk -v a="$pct" -v b="$floor" 'BEGIN{ exit !(a + 0 < b + 0) }'; then
    printf "  %-70s %7s  %s%% [FAIL]\n" "$pkg" "${pct}%" "$floor"
    violations=$((violations + 1))
    continue
  fi

  if [[ "$floor" != "$goal" ]]; then
    printf "  %-70s %7s  %s%% (goal %s%%)\n" "$pkg" "${pct}%" "$floor" "$goal"
    # Debt paid down but not locked in: suggest raising the floor,
    # capped at the §11 goal.
    if awk -v a="$pct" -v b="$floor" 'BEGIN{ exit !(a + 0 >= b + 5) }'; then
      new="$(awk -v a="$pct" -v g="$goal" 'BEGIN{ n = int(a) - 1; if (n > g) n = g; print n }')"
      hints+=("$pkg: floor $floor% -> $new% (now $pct%)")
    fi
  else
    printf "  %-70s %7s  %s%%\n" "$pkg" "${pct}%" "$floor"
  fi
done <<< "$summary"

echo
if [[ ${#hints[@]} -gt 0 ]]; then
  echo "check-coverage: ratchet — these packages cleared their floor by 5pp+:"
  for h in "${hints[@]}"; do
    echo "  $h"
  done
  echo
fi

if [[ $violations -gt 0 ]]; then
  echo "check-coverage: $violations of $policed package(s) below floor"
  exit 1
fi

echo "check-coverage: OK ($policed package(s) at or above floor, $skipped excluded)"
