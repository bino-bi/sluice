<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Server config & secrets

The server config is the YAML file for `sluice serve --config`. Secrets stay out of it: the file holds references. [Configuration](../reference/configuration.md) lists every key.

## Example

Goal: keep the admin token and all secrets out of the file.

```yaml
--8<-- "examples/server-config/server.yaml:admin"
```

```yaml
--8<-- "examples/server-config/server.yaml:secrets"
```

The start script sets them:

```bash
--8<-- "examples/server-config/serve.sh:env"
```

A call without the token:

```bash
--8<-- "examples/server-config/steps/03-admin-without-token.sh"
```

```json
--8<-- "examples/server-config/expected/03-admin-without-token.txt"
```

What happened: `SLUICE_ADMIN__TOKEN` set `admin.token`, so the admin API refuses the call.

## How Sluice loads the config

Later steps win:

1. The built-in defaults.
2. The file from `--config`.
3. Environment variables. `rest.listen` becomes `SLUICE_REST__LISTEN`: prefix `SLUICE_`, upper case, `__` for each dot.
4. The flag `--policies-dir`, which replaces `policies.directory`.

A missing or empty policy directory is no error. Sluice starts and refuses every query.

!!! warning "A variable needs its key"
    Sluice reads a variable only if its key has a default or is in the file. `SLUICE_ADMIN__TOKEN` alone does not set the token: without `token: ""` in the file, the admin API accepts every call. No default: `admin.token`, `mcp.listen`, `identity.apiKeyPepper`, `duckdb.tempDir`, and the keys under `audit.file`, `audit.syslog`, `audit.s3`, `rest.tls`, `admin.tls`.

## Secret references

| Reference | Sluice reads |
|---|---|
| `secret://env/NAME` | The environment variable `NAME`. |
| `secret://file//etc/sluice/key` | The file `/etc/sluice/key`. The path must be absolute. |

There are no other providers. Sluice refuses a secret file that group or others can write.

| Where | Keys that take a reference |
|---|---|
| Server config | `identity.apiKeyPepper`, `audit.file.genesis`, `audit.s3.credentialsRef`, `approval.webhooks[].headersRef`, `mcp.tokenRef`, `mcp.apiKeyRef` |
| `SubjectBinding` | `apiKeys[].hashRef`, `hmacSecretRef` |
| `DataSource` | `credentialsRef`, `headersRef`, `tokenRef` |
| `RelationshipPolicy` | `backend.tokenRef` |
| Column mask arguments | `saltRef`, `keyRef`, `seed` |

`admin.token` holds the token itself.

Sluice reads the secrets of the server config only at start. A [reload](hot-reload.md) reads the secrets of policy objects again. Until then, Sluice caches a mask or data source secret for 10 minutes.

## Validate before you start

```bash
--8<-- "examples/server-config/steps/01-validate.sh"
```

```text
--8<-- "examples/server-config/expected/01-validate.txt"
```

An invalid file ends the command with status 3:

```yaml
--8<-- "examples/server-config/server-bad.yaml:2"
```

```bash
--8<-- "examples/server-config/steps/02-validate-bad.sh"
```

```text
--8<-- "examples/server-config/expected/02-validate-bad.txt"
```

## Limits

- A wrong `--config` path is no error. Sluice then uses the defaults.
- `--strict` checks only policy files for unknown fields.
- `config validate` does not read secrets. A missing secret of the server config stops `sluice serve`.
- Sluice keeps a final newline of a [pepper or genesis](../reference/glossary.md) file. Write these files without one.
- `budget.failClosed` matters only at start. If the budget store does not open, `true` stops Sluice and `false` turns budgets off.

## See also

- [Subjects, keys & budgets](../policies/subjects.md)
- [Key rotation](../security/key-rotation.md)
