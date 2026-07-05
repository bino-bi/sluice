<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# CLI

The `sluice` binary is the entry point to every operation. The full
help text is auto-generated; the table below summarises the
subcommands landing in `v0.1`.

| Subcommand            | Purpose                                                           |
| --------------------- | ----------------------------------------------------------------- |
| `sluice version`      | Print binary identity; `--json` emits the full `Build` struct.    |
| `sluice serve`        | Composition root — starts REST + MCP + admin transports.          |
| `sluice config validate` | Validate a server config + policy directory.                   |
| `sluice policy validate` | Structural validation of a policy directory.                   |
| `sluice policy explain`  | Show the decision for a synthetic user + table.                |
| `sluice policy test`     | Run declarative policy test suites against a policy directory.  |
| `sluice datasource check` | Resolve every DataSource.spec.type to a registered factory.   |
| `sluice schema export`   | Emit JSON Schema 2020-12 for one or all Kinds.                 |
| `sluice audit verify`    | Walk an audit directory and verify the hash chain.             |

## Exit codes

| Code | Meaning                                                          |
| ---- | ---------------------------------------------------------------- |
| 0    | Success.                                                          |
| 1    | I/O, unexpected runtime, or wrong arguments.                      |
| 2    | Registry / factory lookup failure.                                |
| 3    | Validation failure (`config`, `policy validate`).                 |
| 4    | Audit chain broken or missing.                                    |

## Regenerating this page

```bash
make docs-generate
```

Runs `scripts/gen-cli-docs.go` against the compiled binary to emit
every cobra subtree, flag, default, and example. The CI check in
`docs-deploy.yaml` fails if the generated output drifts from the
committed page.
