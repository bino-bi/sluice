<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Observability

Sluice writes logs, serves Prometheus metrics and sends traces. The record of every query is the [audit log](../security/audit.md).

## Example

Goal: find out why a query failed.

```bash
--8<-- "examples/observability/steps/02-failed-query.sh"
```

```json
--8<-- "examples/observability/expected/02-failed-query.txt"
```

Search the log for this error:

```bash
--8<-- "examples/observability/steps/03-log.sh"
```

```text
--8<-- "examples/observability/expected/03-log.txt"
```

What happened: the client gets an error code and a `query_id`. The log line with this `query_id` holds the cause from DuckDB.

## Logs

Sluice writes one line per event to stderr.

```yaml
--8<-- "examples/observability/server.yaml:logging"
```

- `logging.level` is `debug`, `info`, `warn` or `error`. `logging.format` is `json` or `text`.
- Sluice accepts an unknown value. It then uses `info` or `json`.
- `ERR_QUERY_FAILED` logs a `WARN` line. `ERR_INTERNAL` logs an `ERROR` line with the message `internal error`.
- These lines are not redacted. They can hold SQL and cell values, like `'Alice'` above.
- `logging.maxErrorBytes` cuts a long `error` text. The default is 4096, and `0` turns the cut off.

## Metrics

The admin listener serves `GET /metrics`. It needs `admin.enabled: true` and the admin token.

```bash
--8<-- "examples/observability/steps/04-metrics.sh"
```

```text
--8<-- "examples/observability/expected/04-metrics.txt"
```

[Metrics](../reference/metrics.md) lists every metric. Two alerts are useful:

- `sluice_audit_dropped_total` above 0: Sluice lost audit records. The `reason` label is `queue_full`, `context`, `buffer_full` or `write_error`.
- `sluice_audit_queue_depth` close to 10000: the audit queue is almost full.

## Tracing

Add a `tracing` block to the server config. Sluice then sends spans to an OpenTelemetry (OTLP) collector. A span is one timed step of a request.

```yaml
--8<-- "examples/observability/tracing.yaml:2"
```

- `protocol` is `grpc` or `http`. `insecure: true` sends without TLS.
- Sluice continues the trace of a W3C `traceparent` request header.
- Every REST request and every MCP request over HTTP gets a span.
- A query adds the span `query` with the children `query.parse`, `query.policy`, `query.rewrite` and `query.execute`. A post-query [column mask](../policies/column-masks.md) adds `query.mask`.
- The `query` span holds the query id, origin, decision, SQL fingerprint, catalogs and error code, never the SQL text. It ends before Sluice sends the rows.

## See also

- [Metrics reference](../reference/metrics.md)
- [Audit log](../security/audit.md)
- [Deployment](deployment.md)
