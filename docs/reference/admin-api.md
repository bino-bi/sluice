<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Admin API

The admin plane is a separate HTTP listener for operators: policy snapshot
inspection, datasource status, access debugging, reload, audit tail, pending
approvals, and Prometheus metrics. It is **off by default**; enable it with
`admin.enabled: true` and bind it via `admin.listen` (default `:9091`).

## Authentication

Every endpoint sits behind a single static bearer token (`admin.token`),
compared in constant time:

```console
$ curl -s -H "Authorization: Bearer $ADMIN_TOKEN" http://localhost:9091/admin/healthz
```

An empty `admin.token` disables auth — dev mode — and the server logs a loud
warning at boot ("running without an auth token — do not expose this port").
Failed auth returns `401` with `WWW-Authenticate: Bearer realm="sluice-admin"`.
Responses echo an `X-Admin-Request-Id` header.

!!! warning "Not yet implemented: TLS on the admin listener — rejected at load"
    The admin listener always serves plain HTTP, so setting `admin.tls`
    **fails validation**: `sluice config validate` exits 3 and
    `sluice serve` refuses to start. Terminate TLS in front of it, as the
    danger admonition below recommends.

!!! danger "Never expose the admin listener publicly"
    The admin plane reads audit records and policy internals and can trigger
    reloads. Bind it to localhost or a management network, always set a
    token, and terminate TLS in front of it. Do not port-forward it to the
    internet.

## GET /admin/policies

Returns the live compiled snapshot: `version`, `digest`, policies grouped by
kind (each `{name, priority, enforcement}`), and any loader `warnings`.

```console
$ curl -s -H "Authorization: Bearer $ADMIN_TOKEN" http://localhost:9091/admin/policies
{"version":3,"digest":"9f2c…","policies":{"SqlAccessPolicy":[{"name":"allow-analytics","priority":100,"enforcement":"Enforce"}]}}
```

## GET /admin/datasources

Status list for every attached source: `{name, type, healthy, last_check,
last_error, latency_ms, schema_pulled_at}`.

```console
$ curl -s -H "Authorization: Bearer $ADMIN_TOKEN" http://localhost:9091/admin/datasources
```

## GET /admin/subjects/explain

Answers "why can (or can't) this subject query this table?" without running
a query. Query parameters:

| Parameter | Required | Meaning |
|---|---|---|
| `user` | yes | Synthetic subject identifier |
| `table` | yes | Target table, `catalog.schema.table` |
| `issuer` | no | Synthetic `iss` claim |
| `groups` | no | Comma-separated groups |
| `claims` | no | `key=value`, repeatable |
| `simulated_sql` | no | Candidate SQL to evaluate instead of the bare table |

Debugging walkthrough — a user reports a denied query:

```console
$ curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
    "http://localhost:9091/admin/subjects/explain?user=alice&table=shop.main.customers&groups=analytics&claims=tenantId=acme"
```

The response mirrors `sluice policy explain --json`: the effective decision,
matched and rejected policies, row filters, and column masks. Add
`simulated_sql=SELECT …` to evaluate a specific statement shape. If the
decision is `deny` with no matched policies, no allow rule covers the table
— default-deny applies (see [Matching & precedence](../policies/matching.md)).

## POST /admin/reload

Triggers a validate-then-swap policy reload — the cross-platform equivalent
of `SIGHUP`. Success returns `{"ok": true, "digest": "…"}`; a failed reload
returns `ERR_CONFIG_INVALID` (load/decode failure) or `ERR_POLICY_INVALID`
(policy compile failure) and the previous snapshot stays active. Works
regardless of `policies.reload`, which gates only the fsnotify watcher.

```console
$ curl -s -X POST -H "Authorization: Bearer $ADMIN_TOKEN" http://localhost:9091/admin/reload
```

See [Configuration reload](../operations/hot-reload.md).

## GET /admin/audit/tail?n=

Streams the last `n` audit records as NDJSON (`application/x-ndjson`). `n`
defaults to 100 and is capped at 1000; `n` below 1 is a `400`.

```console
$ curl -s -H "Authorization: Bearer $ADMIN_TOKEN" "http://localhost:9091/admin/audit/tail?n=20"
```

Records come back in chronological order (oldest → newest), read across
the file sink's rotated JSONL files. Buffered writes are flushed first,
so just-recorded entries are visible; a partial line being appended
concurrently is skipped. See [Audit trail](../security/audit.md).

## GET /admin/approvals

Lists pending approval requests as `{"pending": [...], "count": N}`. The
view is **token-free** — it never contains the accept/reject capability
tokens, so read access here does not grant decision power. Returns `501`
when no approval broker is configured.

## GET /admin/healthz

Admin replica of `/v1/ready`, plus the config digest and version so
operators can watch hot reloads land: `{status, version, config_digest,
config_version}`. Returns `503` with `"status": "degraded"` when any
datasource is unhealthy.

## GET /admin/version

Build identity: `{version, commit, build_time, go}`.

## GET /metrics

Prometheus metrics in text exposition format, behind the same bearer token.
Configure the scrape with a credentials file:

```yaml
# fragment — Prometheus scrape config, not a Sluice policy
scrape_configs:
  - job_name: sluice
    static_configs: [{ targets: ["localhost:9091"] }]
    authorization:
      credentials: "<admin token>"
```

The metric catalogue is generated into [Metrics](metrics.md).

## Endpoints that can return 501

`GET /admin/approvals` (no approval broker) responds `501 Not
Implemented` when its dependency is not wired, so clients can distinguish
"not enabled in this deployment" from an error. `POST /admin/reload` and
`GET /admin/audit/tail` are always wired under `sluice serve`; their
`501` paths remain only for embedders composing the admin server without
a reloader or file sink.
