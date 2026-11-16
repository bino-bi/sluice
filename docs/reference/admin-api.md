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
| `GET` | `/admin/datasources` | Shows the state of each data source. | `datasources` with `name`, `type`, `healthy`, `last_check`, `schema_pulled_at`. `last_error`, `latency_ms` and `status` only when set |
| `GET` | `/admin/subjects/explain` | Shows which policies apply to a subject. | See [Explain](#explain) |
| `POST` | `/admin/reload` | Loads the policy directory again. | `ok`, `digest` |
| `GET` | `/admin/audit/tail` | Returns the newest audit records. | One JSON record per line |
| `POST` | `/admin/synthetic/{name}/build` | Starts a build of a synthetic data source. | 202 with `name`, `build_id`, `state`, `already_running`. See [Synthetic builds](#synthetic-builds) |
| `POST` | `/admin/synthetic/{name}/cancel` | Asks the running build of a synthetic data source to stop. | 202 with `name`, `build_id`, `state`. See [Synthetic builds](#synthetic-builds) |
| `GET` | `/admin/synthetic/{name}` | Shows the state, the builds and the report of a synthetic data source. | `name`, `source`, `status`, `spec_hash`, `current`, `builds`, `report` |
| `GET` | `/admin/approvals` | Lists the pending approvals, without tokens. | `pending`, `count`. 501 when approvals are off |
| `GET` | `/admin/healthz` | Reports the health of the server. | `status`, `version`, `config_digest`, `config_version`. 503 when a data source is unhealthy |
| `GET` | `/admin/version` | Reports the build. | `version`, `commit`, `build_time`, `go` |
| `GET` | `/metrics` | Serves the [metrics](metrics.md). | Prometheus text |

Every response has an `X-Admin-Request-Id` header.

## Explain

| Parameter | Meaning |
|---|---|
| `user` | Subject id. Required. |
| `table` | Table as `catalog.schema.table`. Required. Sluice does not add a missing catalog or schema. A policy that names them does not match a shorter name. |
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

## Synthetic builds

`POST /admin/synthetic/{name}/build` answers at once. The build runs in the server and replaces the snapshot when it succeeds. See [Synthetic data](../operations/synthetic-data.md).

- A second call during a build returns the running build with `already_running: true`. A retry is harmless.
- One build runs at a time in the server. A build of another name during a build gives 409 `ERR_CONFLICT`.
- `POST /admin/synthetic/{name}/cancel` asks the running build of that name to stop and answers 202 at once. A build that stops ends as `failed`, and its end audit record has `ERR_CANCELED`. A build that already published its snapshot ends as `succeeded`. Check `GET /admin/synthetic/{name}` for the end state. Without a running build of that name the call gives 409 `ERR_CONFLICT`.
- A build that runs longer than `synthetic.buildTimeout` (default `1h`) is canceled. It ends as `failed`, and its end audit record has `ERR_TIMEOUT`.
- `status` is `not_built`, `ready` or `stale`. `GET /admin/datasources` shows it too.
- `builds` lists only the builds of this server process. `current` and `report` are `null` when no snapshot exists.
- The `state` of a build is `running`, `succeeded`, `failed` or `published_not_attached`. `published_not_attached` means: the snapshot is current on disk, but the server could not attach it. The server serves it after the next start.
- `current` and `report` show the build that the server serves. After `sluice synth build`, the server serves the old snapshot until a restart or the next server build. `GET` shows that old build.
- An unknown name, or a data source that is not synthetic, gives 404 with an empty body.
- Without a synthetic data source, all three routes give 501.
- With `audit.failClosed`, a build whose start record the audit log does not take gives 503 `ERR_AUDIT_UNAVAILABLE`.

## Limits

- Set `admin.token` in the server config or with `SLUICE_ADMIN__TOKEN`. See [Server config](../operations/server-config.md).
- Without a token, Sluice does not start, unless `admin.listen` binds to loopback only (`127.0.0.1`, `::1` or `localhost`) or `admin.allowAnonymous: true` is set. Then every route is open, and Sluice logs a warning at start.
- A wrong token gives 401 `ERR_UNAUTHORIZED` and a `WWW-Authenticate` header.
- After 10 wrong or missing tokens from one client IP, Sluice answers 429 `ERR_RATE_LIMITED` to that IP, also with the right token. The IP gets one new try every 10 seconds.
- A missing `user` or `table`, or `n` below 1, gives 400 `ERR_SYNTAX`.
- A failed reload gives 400 `ERR_POLICY_INVALID` or `ERR_CONFIG_INVALID`. The old policies stay active.

## See also

- [Reload](../operations/hot-reload.md)
- [Hardening](../security/hardening.md)
- [Audit log](../security/audit.md)
- [Synthetic data](../operations/synthetic-data.md)
