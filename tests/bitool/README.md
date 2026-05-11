<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# BI-tool smoke tests

Opt-in lane that points a handful of popular BI tools at a running
Sluice instance (usually the `hello-sluice` example) and asserts that
the tool can:

1. Connect over the tool's preferred protocol (HTTP/JDBC).
2. Introspect the schema (visible via `describe_table` or the
   information-schema-passthrough that Sluice ships on `/v1/query`).
3. Execute one SELECT and render a non-empty result.

## Tools in scope

- **Metabase** — via the HTTP-SQL connector.
- **Grafana** — via the JSON datasource plugin.
- **DBeaver** — via the REST proxy recipe documented under
  `docs/cookbook/`.

## Running locally

```bash
export BITOOL=1
docker compose -f examples/hello-sluice/docker-compose.yaml up -d
go test ./tests/bitool/ -tags=bitool -run TestMetabase
```

## Gating

This lane is opt-in because it pulls large container images and can
be flaky on shared CI runners. `.github/workflows/nightly.yaml` sets
`BITOOL=1` only on self-hosted runners with the image cache pre-warmed.
