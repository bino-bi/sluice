<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Reference

These pages are the source of truth for every external interface Sluice
exposes: the CLI, the REST data plane, the admin plane, the MCP tools, and
the generated catalogues for the policy schema, configuration, metrics,
and error codes.

!!! warning "Four pages are generated — do not hand-edit them"
    `policy-schema.md`, `configuration.md`, `metrics.md`, and
    `error-codes.md` are produced by `make docs-generate` from the code
    itself. Hand edits are overwritten on the next generation run and
    cause `make docs-check` to fail in CI. Edit the Go sources (or the
    generator scripts under `scripts/`) instead.

| Page | Covers | Maintained |
|---|---|---|
| [CLI](cli.md) | All 12 command paths, flags, exit codes | Hand-written |
| [REST API](rest-api.md) | `POST /v1/query`, approval endpoints, meta endpoints, error envelope | Hand-written |
| [Admin API](admin-api.md) | The `:9091` operator plane, including `/metrics` | Hand-written |
| [MCP tools](mcp.md) | The nine tools over stdio / Streamable HTTP | Hand-written |
| [Policy schema](policy-schema.md) | JSON Schema 2020-12 for all 11 policy kinds | Generated |
| [Configuration](configuration.md) | `server.yaml` fields, defaults, `SLUICE_*` env overrides | Generated |
| [Metrics](metrics.md) | The Prometheus surface on the admin listener | Generated |
| [Error codes](error-codes.md) | The 24-code catalogue with HTTP mappings | Generated |

Related pages outside this section:

- Declarative test suites for `sluice policy test` are documented in
  [Testing policies](../policies/testing.md).
- Policy semantics (matching, precedence, each kind's behaviour) live
  under [Policies](../policies/index.md).
