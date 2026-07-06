<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Operations

Everything you touch when you run Sluice in production: two listeners, one
directory of YAML manifests, an audit directory, and a small amount of local
state.

## The operational surface

| Surface | Default | Notes |
| ------- | ------- | ----- |
| Data listener | `:8080` (`rest.listen`) | `POST /v1/query`, health endpoints, approval links. |
| Admin listener | `:9091` (`admin.listen`) | Off by default (`admin.enabled: false`). Bearer-token protected: `/admin/*` plus `GET /metrics`. |
| Policy directory | `./policies.d` (`policies.directory`) | All policy kinds, `SubjectBinding`, and (in every shipped example) `DataSource` manifests. Hot-reloaded. |
| Data source directory | `./datasources.d` (`datasources.directory`) | Separate directory option for `DataSource` manifests; the examples keep them in `policies.d` instead. |
| Audit directory | `audit.file.path` | Hash-chained JSONL, created `0750`. Unset means `$TMPDIR/sluice-audit` plus a loud warning — never ship that. |
| Budget state | `./state` (`budget.stateDir`) | SQLite usage counters when `budget.enabled: true`. |

## Day-2 tasks

- [Deployment](deployment.md) — building the CGO binary, Docker, systemd,
  file layout, probes.
- [Server config & secrets](server-config.md) — `sluice.yaml`, `SLUICE_*`
  overrides, `secret://` references, pre-deploy validation.
- [Data sources](data-sources.md) — attaching PostgreSQL, MySQL, SQLite,
  DuckDB files, S3 Parquet, and MotherDuck as catalogs.
- [Configuration reload](hot-reload.md) — fsnotify, `SIGHUP`,
  `POST /admin/reload`, and what still needs a restart.
- [Observability](observability.md) — structured logs, Prometheus metrics,
  audit tail, health endpoints.

Related chapters: the [audit trail](../security/audit.md) is the primary
operational record, and [hardening](../security/hardening.md) covers the
security posture beyond day-2 mechanics.

## Production-readiness checklist

- [ ] **Audit is fail-closed and durable.** `audit.file.path` points at
  persistent storage and `audit.failClosed` is left at its default `true` —
  Sluice then refuses to serve a query it cannot audit.
- [ ] **Admin plane is locked down.** `admin.enabled: true`, with
  `token: ""` set in `sluice.yaml` and the real value injected via the
  `SLUICE_ADMIN__TOKEN` environment variable — the env override only takes
  effect when the `admin.token` key exists in the file. An empty effective
  token is dev mode and logs a warning; never commit token literals to
  `sluice.yaml`.
- [ ] **Default-deny verified with a policy test.** Your
  [`sluice policy test`](../policies/testing.md) suite contains at least one
  case expecting `deny` for a subject no policy matches.
- [ ] **Budgets and rate limits on agent bindings.** Every agent-facing
  `SubjectBinding` sets `rateLimit` and `budget`
  (see [Subjects, keys & budgets](../policies/subjects.md)), and the server
  config sets `budget.enabled: true`.
- [ ] **Policy tests run in CI.** `sluice config validate <dir> --config
  sluice.yaml --strict` and `sluice policy test <dir>` gate every change
  before it reaches the policy directory.
- [ ] **Secrets are references.** Peppers, key hashes, credentials, and the
  audit genesis seed are `secret://` references, never literals in YAML.

Field-level defaults live in the generated
[configuration reference](../reference/configuration.md).
