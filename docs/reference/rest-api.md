<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# REST API

The REST data plane listens on a single port (default `:8080`) and
exposes five endpoints.

| Method | Path              | Purpose                                            |
| ------ | ----------------- | -------------------------------------------------- |
| `POST` | `/v1/query`       | Execute a SQL statement through the policy engine. |
| `GET`  | `/v1/health`      | Liveness — 200 as soon as the listener is up.      |
| `GET`  | `/v1/ready`       | Readiness — 200 when every dependency is wired.    |
| `GET`  | `/v1/version`     | The same JSON as `sluice version --json`.          |
| `GET`  | `/openapi.json`   | Machine-readable OpenAPI 3.1 document.             |

## `POST /v1/query`

Request body:

```json
{
  "sql":    "SELECT id, amount FROM orders WHERE region = 'eu'",
  "format": "json"
}
```

Headers:

- `Authorization: Bearer <JWT>` or `Authorization: ApiKey <id>.<material>`
- `Content-Type: application/json`
- `Accept: application/json` (default), `text/csv`, or
  `application/vnd.apache.arrow.stream` (rejected in MVP).

Response headers (always):

- `X-Query-Id` — the ULID that ties the response to the audit record.
- `X-Sluice-Row-Count`, `X-Sluice-Truncated` — on CSV responses.

Errors use `pkg/errors.APIError` JSON with a canonical HTTP status
(400, 401, 403, 408, 413, 500). The `code` field carries the machine
identifier; see [Error codes](error-codes.md).

## Streaming

JSON responses flush every 100 rows so long result sets reach clients
progressively. CSV is RFC 4180 and streams row-by-row. Arrow streaming
lands once the executor's Arrow iterator ships (tracked for v0.2).
