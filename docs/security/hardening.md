<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Hardening

Check these settings before production use.

## Example

Goal: a daily budget for one API key.

```yaml
--8<-- "examples/hardening/policies.d/binding-report-bot.yaml:2"
```

In the server config:

```yaml
--8<-- "examples/hardening/server.yaml:budget"
```

The client sends this query twice. The second answer:

```bash
--8<-- "examples/hardening/steps/02-over-budget.sh"
```

```json
--8<-- "examples/hardening/expected/02-over-budget.txt"
```

What happened: the first query returned 2 rows and used up `rowsPerDay`. Sluice refused the second query.

## Checklist

| Setting | Why | Value |
|---|---|---|
| `admin.token` | Without it the admin API accepts every request | In the server config file. `SLUICE_ADMIN__TOKEN` alone does not set it |
| `admin.listen` | Serves the admin API and `/metrics` | A private address |
| `rest.tls`, `admin.tls` | REST and admin serve plain HTTP by default | See [Deployment](../operations/deployment.md) |
| `mcp.listen` | MCP over HTTP: no `tls` setting, no rate limit before login | A TLS proxy in front, or stdio |
| `rest.tls.clientCA` | Adds a client certificate check. Login stays required | Your CA bundle |
| `identity.apiKeyPepper` | API keys do not work without the [pepper](../reference/glossary.md) | A `secret://` reference |
| Secret files | Sluice refuses files that group or others can write | `chmod 0600` |
| `audit.file.path`, `audit.file.genesis` | Defaults: a temp directory, a [genesis](../reference/glossary.md) built from host name and build commit | A fixed directory, a `secret://` reference |
| JWT subject binding: `issuer`, `audience`, `jwksUrl` | The audience is checked only when set | Set all three |
| Subject binding: `rateLimit`, `budget` | Both need `claims.subjectId` or `issuer`. Budgets also need `budget.enabled: true` | See the example |
| `resources` in every allow policy | Without `resources`, an allow policy covers every table | List the tables |
| `QueryRewritePolicy` on `tables: ["**"]` | Caps result rows and run time of every query. One aggregate row can hold a whole table | See [Rejects and rewrites](../policies/guardrails.md) |
| `credentialsRef` of a data source | Sluice only reads | A read-only database user |
| `s3` `bucket`, `http` `urls` | DuckDB can read the whole bucket and every listed URL | Only what clients need |
| `headersRef` | Every URL of the data source gets the same headers | One data source per credential |
| Default deny | A policy change must not open a table | A [policy test](../policies/testing.md) that expects `deny` |

## See also

- [Threat model](threat-model.md)
- [Key rotation](key-rotation.md)
