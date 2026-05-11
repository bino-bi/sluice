<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# Benchmarks

This directory holds the release-telemetry benchmarks — the ones CI
upload to the main-branch baseline for `benchstat` comparisons. The
in-tree unit benchmarks (under `internal/*/bench_test.go`) stay next
to the code they measure; only cross-package or workload-level
benchmarks belong here.

## Running locally

```bash
make bench
# or for a specific scenario:
go test ./benchmarks/ -bench=. -benchmem -benchtime=1s
```

## What belongs here

- **Workload benchmarks.** E.g. "500 requests through the full
  parse → policy → rewrite → execute → audit pipeline with 10 policy
  objects loaded."
- **Cross-package comparisons.** E.g. `pg_query` vs. `pure_parser`
  throughput on the same fixture set.

## What does NOT belong here

- Micro-benchmarks of a single function — those stay in the package.
- Anything that requires Docker to be running — that's an integration
  or e2e test, not a benchmark.
