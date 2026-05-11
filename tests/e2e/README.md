<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# End-to-end tests

The e2e lane spins up an entire example stack (`examples/*/docker-compose.yaml`)
and drives it with a test harness that asserts the golden outcomes
documented in each example's README.

## Running locally

```bash
cd examples/hello-sluice
docker compose up -d --build
# then from the repo root:
go test ./tests/e2e/ -tags=e2e -run TestHelloSluice
```

The compose stacks bind-mount policies and seed data read-only, so
tests can tear down and reset without losing local state. Audit logs
write under `examples/*/data/audit/` — reset between runs with
`docker compose down -v`.

## CI

`.github/workflows/nightly.yaml` runs one e2e matrix entry per example
scenario. Gate failures raise an issue automatically via the
`gh issue create` step.
