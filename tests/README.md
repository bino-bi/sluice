<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# Test lanes

The PR lane runs unit tests only (`make test`, i.e. `go test -race
-short ./...`). Everything that needs Docker, a network, or more than
a few seconds of wall time lives here.

| Lane                 | Trigger       | What runs                                        |
| -------------------- | ------------- | ------------------------------------------------ |
| `tests/integration/` | `-tags=integration` + `make test-integration` | Testcontainers (Postgres, MySQL, MinIO) exercising the driver + executor + rewrite path. Nightly CI runs the full matrix. |
| `tests/e2e/`         | nightly       | Full stack via `examples/*/docker-compose.yaml` — REST + MCP + admin transports against real seeded data. |
| `tests/bitool/`      | nightly       | Smoke tests that point Metabase / Grafana / DBeaver at Sluice and assert the server's reported shape. Gated — operators opt in by setting `BITOOL=1`. |

Each subdirectory carries its own README explaining what to expect
and how to run that lane locally.
