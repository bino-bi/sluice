<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# CLI

The `sluice` binary is a single executable with 12 command paths. `serve` is
the composition root; every other command is self-contained and read-only.

## Global behavior

**Config resolution.** Commands that accept `--config` load the given
`sluice.yaml`; there is no default search path. A missing file is not an
error (defaults and environment overlays still apply); a malformed file is
fatal.

**Environment overrides.** Every `server.yaml` field has a `SLUICE_`-prefixed
override, with `.` mapped to `__` (`SLUICE_REST__LISTEN=:9000` overrides
`rest.listen`). See [Configuration](configuration.md).

**Exit codes.**

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | User/input or I/O error (bad flags, unreadable files) |
| 2 | Transport or datasource failure |
| 3 | Policy validation, compile, or test failure (CI gate signal) |
| 4 | Audit hash chain broken |

## serve

Starts the REST transport (always), plus the MCP and admin transports when
enabled in config. `SIGINT`/`SIGTERM` shut down gracefully; `SIGHUP` triggers
a policy reload. Exits 1 on startup failure, 2 on a transport error. See
[Deployment](../operations/deployment.md).

| Flag | Default | Meaning |
|---|---|---|
| `--config` | (none) | Path to `sluice.yaml`; defaults apply when omitted |
| `--policies-dir` | (none) | Override `policies.directory` from `sluice.yaml` |

```console
$ sluice serve --config sluice.yaml --policies-dir ./policies.d
```

## mcp

Runs only the MCP surface for AI agents (stdio by default): a static
credential is verified once at startup and pinned onto every tool call; with
`--transport streamable_http`, every request is authenticated instead. Exits
1 on startup or auth failure. See [MCP tools](mcp.md).

| Flag | Default | Meaning |
|---|---|---|
| `--config` | (none) | Server config file (`SLUICE_*` env also applies) |
| `--policies-dir` | (none) | Policy directory (overrides `policies.directory`) |
| `--transport` | `stdio` | `stdio` or `streamable_http` |
| `--jwt` | (none) | Static JWT bearer token (stdio); falls back to `SLUICE_MCP_TOKEN` |
| `--api-key` | (none) | Static API key `<id>.<secret>` (stdio) |
| `--allow-anonymous` | `false` | Run without a credential; queries stay default-denied |

```console
$ sluice mcp --config server.yaml --policies-dir policies.d --jwt "$SLUICE_MCP_TOKEN"
```

## version

Prints the build identity (version, commit, build time, Go version). Exits 0.

| Flag | Default | Meaning |
|---|---|---|
| `--json` | `false` | Emit version metadata as JSON |

```console
$ sluice version --json
```

## config validate

`sluice config validate [<policy-dir>]` validates the server config (when
`--config` is given) and every policy manifest under `<policy-dir>`,
surfacing all decoding and structural errors. Server-config settings this
build cannot enforce (mTLS fields, `admin.tls`, `datasources.reload`,
unimplemented `secret://` providers) are rejected with exit 3 — the same
check that makes `sluice serve` refuse to start. Exits 0/1/3. See
[Server config & secrets](../operations/server-config.md).

| Flag | Default | Meaning |
|---|---|---|
| `--config` | (none) | Path to `sluice.yaml` (optional) |
| `--strict` | `false` | Reject unknown YAML fields |

```console
$ sluice config validate ./policies.d --config sluice.yaml --strict
```

## policy validate

`sluice policy validate <policy-dir>` runs the server's load pipeline plus
the policy compiler, so a schema-valid manifest that uses an unsupported
feature fails here — with a precise kind/name/field message — rather than
during a live reload. Exits 0/1/3. See [Policies](../policies/index.md).

| Flag | Default | Meaning |
|---|---|---|
| `--strict` | `false` | Reject unknown YAML fields |

```console
$ sluice policy validate ./policies.d --strict
```

## policy explain

Builds a synthetic subject and reports which policies match the target
table: the effective decision, row filters, and column masks. `--user` plus
one of `--table`/`--sql` are required. Exits 0, 1 (bad flags or load
failure), or 3 (compile failure). See
[Matching & precedence](../policies/matching.md).

| Flag | Default | Meaning |
|---|---|---|
| `--policies-dir` | `./policies.d` | Directory containing policy manifests |
| `--user` | (required) | Synthetic subject identifier |
| `--issuer` | (none) | Synthetic issuer (`iss` claim) |
| `--email` | (none) | Synthetic email claim |
| `--groups` | (none) | Subject groups, comma-separated |
| `--claims` | (none) | Extra claims as `key=value`, comma-separated |
| `--table` | (none) | Target table as `catalog.schema.table` |
| `--sql` | (none) | Reserved — see below |
| `--json` | `false` | Emit the explain result as JSON |

!!! warning "Not yet implemented"
    `--sql` is accepted but reserved — SQL simulation is not wired into
    this command yet. Use `--table`.

```console
$ sluice policy explain --user alice --groups analytics --table shop.main.customers
```

## policy test

Compiles the policies under `<policy-dir>` and runs every declarative test
case, asserting outcome, filters, masks, applied policies, and rewritten
SQL. Exits 3 on compile failure, any case failure, or zero cases. See
[Testing policies](../policies/testing.md).

| Flag | Default | Meaning |
|---|---|---|
| `--tests` | `<policy-dir>/tests` | Suite file or directory |
| `--strict` | `false` | Reject unknown YAML fields in policies |
| `--json` | `false` | Emit the report as JSON |

```console
$ sluice policy test ./policies.d --json
```

## datasource check (alias: ds)

`sluice datasource check [name]` loads the DataSource manifests and reports
whether each declared `spec.type` has a registered factory, plus the
schema/table filters. Live connection probes are the job of `serve` and its
health loop. Exits 1 on I/O error, 2 when any source fails. See
[Data sources](../operations/data-sources.md).

| Flag | Default | Meaning |
|---|---|---|
| `--dir` | `./policies.d` | Directory containing DataSource manifests |
| `--json` | `false` | Emit the report as JSON |

```console
$ sluice ds check warehouse --dir ./policies.d
```

## apikey hash

Prints the hex HMAC-SHA256 of `id + ":" + material` under the pepper — the
value a SubjectBinding `apiKeys[].hashRef` must resolve to. All three flags
are required. See [Subjects, keys & budgets](../policies/subjects.md).

| Flag | Default | Meaning |
|---|---|---|
| `--pepper` | (required) | Server pepper — raw value or `secret://` URI (env, file) |
| `--id` | (required) | Public key identifier |
| `--material` | (required) | Key material presented by the caller |

```console
$ sluice apikey hash --pepper secret://env/SLUICE_PEPPER --id ci-bot --material "$KEY"
```

## schema export

Prints JSON Schema (draft 2020-12) for the policy YAML surface: a `oneOf`
union across all 11 kinds, or a single kind with `--kind`. Suitable for IDE
YAML validation and CI linters. See [Policy schema](policy-schema.md).

| Flag | Default | Meaning |
|---|---|---|
| `--kind` | (none) | Export only this kind (e.g. `SqlAccessPolicy`) |

```console
$ sluice schema export --kind ColumnMaskPolicy > columnmask.schema.json
```

## audit verify

`sluice audit verify <audit-dir>` walks every `*.jsonl` file in filename
order, recomputing each record's hash and carrying the prior hash across
file boundaries. Exits 4 when the chain is broken (tampering, a missing
record, or an anchor mismatch). See [Audit trail](../security/audit.md).

| Flag | Default | Meaning |
|---|---|---|
| `--anchor` | (none) | Pin the genesis record's `prior_hash` (`sha256(seed)`) |
| `--json` | `false` | Emit the report as JSON |

```console
$ sluice audit verify ./data/audit --anchor "$SLUICE_AUDIT_GENESIS"
```

## budget show

`sluice budget show <subject>` reports a subject's recorded CPU seconds and
rows for one UTC day (default today). See
[Subjects, keys & budgets](../policies/subjects.md).

| Flag | Default | Meaning |
|---|---|---|
| `--state-dir` | `./state` | Budget state directory (contains `budget.db`) |
| `--day` | today | UTC day, `YYYY-MM-DD` |
| `--json` | `false` | Emit JSON |

```console
$ sluice budget show alice --day 2026-07-06
```
