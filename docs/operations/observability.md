<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Observability

Three surfaces — logs, metrics, and (upcoming) traces.

## Logs

Structured JSON by default via `slog`. Every request logs once with
`request_id`, `subject`, `issuer`, `decision`, `row_count`, and
`duration`. Secret bytes flow through a `telemetry.Redacted{}` wrapper
so they never reach the log line.

```
{"time":"2026-04-20T13:15:00Z","level":"INFO","msg":"query",
 "request_id":"01H…","subject":"alice","decision":"allow",
 "row_count":42,"duration":"12.3ms"}
```

Switch to plain text with `SLUICE_LOG_FORMAT=text`. Valid levels are
`DEBUG`, `INFO`, `WARN`, `ERROR`.

## Metrics

Scraped via Prometheus on `/metrics` (admin port). See
[Reference → Metrics](../reference/metrics.md) for the full surface. A
bare-minimum dashboard watches:

- `rate(sluice_policy_evaluations_total{outcome="deny"}[5m])`
- `histogram_quantile(0.95, sluice_policy_eval_duration_seconds_bucket)`
- `sluice_audit_queue_depth` (alert when > 80% of capacity)
- `sluice_audit_dropped_total` (alert on any non-zero)

## Tracing (roadmap)

OTel tracing hooks are declared; initialisation lands with Slice 2's
telemetry follow-ups. Spans will cover parse → identify → policy →
rewrite → execute → audit with the query ID threaded through as
`sluice.query_id`.
