<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Observability

Three signals: structured logs on stderr, Prometheus metrics on the admin
listener, and — the primary one for a policy gateway — the audit trail.

## Logging

Sluice logs structured JSON via `slog` to **stderr**; stdout stays clean
for data (the MCP stdio transport owns it). Tune with:

```yaml
# fragment — server config excerpt
logging:
  level: info    # debug | info | warn | error
  format: json   # json | text
```

Secret values never reach a log line: sensitive material is wrapped in the
`telemetry.Redacted` helper, which renders as `[redacted]` in every
handler. This guarantee covers logs, panics, metrics, and audit records.

## Metrics

Prometheus text-format metrics are served at `GET /metrics` on the
**admin** listener (default `:9091`), behind the same bearer token as the
rest of the admin plane. `admin.enabled` must be `true`; set a token — an
empty token serves `/metrics` (and the whole admin plane) without
authentication (dev mode).

```bash
curl -s -H "Authorization: Bearer $SLUICE_ADMIN_TOKEN" \
  http://localhost:9091/metrics | head
```

Scrape config:

```yaml
# fragment — prometheus.yml excerpt
scrape_configs:
  - job_name: sluice
    static_configs:
      - targets: ["sluice.internal:9091"]
    authorization:
      type: Bearer
      credentials_file: /etc/prometheus/sluice-admin-token
```

The full catalog (policy evaluations, denials, audit queue depth, approval
gauges, schema cache) is generated from source in the
[metrics reference](../reference/metrics.md). A minimal alert set:
`sluice_audit_dropped_total > 0`, rising
`sluice_policy_denials_total`, and `sluice_audit_queue_depth` near
capacity.

## Audit trail

Every query produces hash-chained JSONL records — decision, subject,
fingerprints, policies applied, row counts. For live inspection the admin
plane tails the current file as NDJSON:

```bash
curl -s -H "Authorization: Bearer $SLUICE_ADMIN_TOKEN" \
  "http://localhost:9091/admin/audit/tail?n=200" \
  | jq -r 'select(.decision == "deny")
           | [.timestamp, .subject.id, .error_code, .sql_fingerprint]
           | @tsv'
```

`n` defaults to 100 (max 1000). Record schema, chain verification
(`sluice audit verify`), and retention are covered in the
[audit trail](../security/audit.md) chapter.

## Health endpoints

| Endpoint | Listener | Purpose |
| -------- | -------- | ------- |
| `GET /v1/health` | data | Liveness. |
| `GET /v1/ready` | data | Readiness; `503` lists unhealthy data sources. |
| `GET /v1/version` | data | Build/version info. |
| `GET /admin/healthz` | admin | Admin-plane health (token required). |
| `GET /admin/version` | admin | Version, for fleet inventory. |

## What is not there yet

!!! warning "Not yet implemented"
    There is no OpenTelemetry tracing — no spans are emitted. Audit sinks
    other than `file` (`s3`, `postgres`, `syslog`, `otlp`) parse in an
    `AuditSink` manifest but are not implemented; only the file sink
    writes records.
