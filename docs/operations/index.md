<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Operations

This section shows how to run Sluice. As an operator, you handle these parts:

| Part | Server config key | Default | What it is |
|---|---|---|---|
| REST listener | `rest.listen` | `:8080` | Queries, health endpoints and approval links. See [Deployment](deployment.md). |
| Admin listener | `admin.listen` | `:9091`, off | The [Admin API](../reference/admin-api.md) and `/metrics`. Turn it on with `admin.enabled: true` and set a [token](server-config.md). |
| Policy directory | `policies.directory` | `./policies.d` | Policies, subject bindings and [data sources](data-sources.md). Sluice [reloads](hot-reload.md) it. |
| Audit directory | `audit.file.path` | None | The [audit log](../security/audit.md). Sluice creates it with mode `0750`. |
| State directory | `budget.stateDir` | `./state` | Budget counters, with `budget.enabled: true`. Sluice creates it with mode `0700`. |

- Without `audit.file.path`, Sluice writes the audit log to the temp folder of the system and logs a warning.
- Every `.yaml` and `.yml` file in the policy directory must be a policy object. Sluice skips hidden folders and the folders `tests` and `testdata`.

## Pages

- [Deployment](deployment.md): image, ports, health endpoints, TLS, signals.
- [Server config & secrets](server-config.md): how Sluice loads the config and reads secrets.
- [Data sources](data-sources.md): the `DataSource` types.
- [Configuration reload](hot-reload.md): change policies without a restart.
- [Observability](observability.md): logs, metrics and tracing.

For a production checklist, see [Hardening](../security/hardening.md).
