<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Admin API

The operator uses the admin API to inspect a running server and to reload its policies. It is off by default and has its own port.

## Example

Turn it on in the server config. Add `admin.tls` to serve HTTPS.

```yaml
--8<-- "examples/admin-api/server.yaml:admin"
```

This call reloads the policy directory. It also works when `policies.reload` is `false`.

```bash
--8<-- "examples/admin-api/steps/04-reload.sh"
```

```json
--8<-- "examples/admin-api/expected/04-reload.txt"
```

## Routes

Every route needs the header `Authorization: Bearer <token>` with the value of `admin.token`.

| Method | Path | What it does | Response |
|---|---|---|---|
| `GET` | `/admin/policies` | Lists the loaded policies. | `version`, `digest`, `warnings`, and `policies` by policy kind with `name`, `priority`, `enforcement` |
| `GET` | `/admin/datasources` | Shows the state of each data source. | `datasources` with `name`, `type`, `healthy`, `last_check`, `schema_pulled_at`. `last_error` and `latency_ms` only when set |
| `GET` | `/admin/subjects/explain` | Shows which policies apply to a subject. | See [Explain](#explain) |
| `POST` | `/admin/reload` | Loads the policy directory again. | `ok`, `digest` |
| `GET` | `/admin/audit/tail` | Returns the newest audit records. | One JSON record per line |
| `GET` | `/admin/approvals` | Lists the pending approvals, without tokens. | `pending`, `count`. 501 when approvals are off |
| `GET` | `/admin/healthz` | Reports the health of the server. | `status`, `version`, `config_digest`, `config_version`. 503 when a data source is unhealthy |
| `GET` | `/admin/version` | Reports the build. | `version`, `commit`, `build_time`, `go` |
| `GET` | `/metrics` | Serves the [metrics](metrics.md). | Prometheus text |

Every response has an `X-Admin-Request-Id` header.

## Explain

| Parameter | Meaning |
|---|---|
| `user` | Subject id. Required. |
| `table` | Table as `catalog.schema.table`. Required. A shorter name is matched as written, so a policy that names the schema does not match it. |
| `issuer` | Issuer of the subject. |
| `groups` | Groups, separated by commas. |
| `claims` | One `key=value` pair. Repeat the parameter for more. |
| `simulated_sql` | A statement. Sluice explains its tables and `table` together. |

```bash
--8<-- "examples/admin-api/steps/03-explain.sh"
```

```json
--8<-- "examples/admin-api/expected/03-explain.txt"
```

`sluice policy explain --json` prints the same shape. When no policy allows the table, `decision` is `deny` and `rejected` names the reason.

## Audit tail

`/admin/audit/tail?n=1` returns the newest record, here the record of the explain call:

```bash
--8<-- "examples/admin-api/steps/05-audit-tail.sh"
```

```json
--8<-- "examples/admin-api/expected/05-audit-tail.txt"
```

`n` is 100 by default and at most 1000. The oldest record comes first. Sluice reads rotated files too.

## Limits

- Set `admin.token` in the server config. `SLUICE_ADMIN__TOKEN` alone does not set it. See [Server config](../operations/server-config.md).
- With an empty token, every route is open. Sluice logs a warning at start.
- A wrong token gives 401 `ERR_UNAUTHORIZED` and a `WWW-Authenticate` header.
- A missing `user` or `table`, or `n` below 1, gives 400 `ERR_SYNTAX`.
- A failed reload gives 400 `ERR_POLICY_INVALID` or `ERR_CONFIG_INVALID`. The old policies stay active.

## See also

- [Reload](../operations/hot-reload.md)
- [Hardening](../security/hardening.md)
- [Audit log](../security/audit.md)
