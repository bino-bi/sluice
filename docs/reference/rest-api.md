<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# REST API

The REST data plane listens on `rest.listen` (default `:8080`). Every request
passes through request-ID tagging (`X-Request-Id`), a body cap
(`rest.maxBodyBytes`, default 1 MiB), a request timeout
(`rest.requestTimeout`, default 30 s), and panic recovery.

## POST /v1/query

Executes one SQL statement through the full pipeline — parse, policy
evaluation, rewrite, DuckDB execution, audit — and streams the result.

**Authentication** (one of):

| Scheme | Header |
|---|---|
| JWT bearer | `Authorization: Bearer <jwt>` |
| API key | `X-Api-Key: <id>.<material>` |
| API key (alt) | `Authorization: ApiKey <id>.<material>` |

A missing or invalid credential yields `401` with
`{"code":"ERR_UNAUTHORIZED", ...}` and a `WWW-Authenticate: Bearer` header.

**Request body** (unknown fields are rejected):

| Field | Type | Meaning |
|---|---|---|
| `sql` | string | The statement to execute (required) |
| `params` | array | Positional query parameters |
| `max_rows` | int | Row cap for this query (default `limits.maxRows`, clamped to `limits.maxRowsCeiling`) |
| `timeout_ms` | int | Query timeout in milliseconds (clamped to `limits.maxQueryTimeout`) |
| `format` | string | `json` (default), `csv`, or `arrow` (rejected — see below) |
| `meta` | object | String map attached to the request |

When `format` is absent, the `Accept` header is consulted:
`application/json` (or `*/*`) selects JSON, `text/csv` selects CSV.

**JSON response** — streamed as a single object, rows emitted one at a time:

```json
{
  "query_id": "01H…",
  "columns": ["id", "name"],
  "rows": [[1, "a"], [2, "b"]],
  "row_count": 2,
  "truncated": false
}
```

**CSV variant** — `Content-Type: text/csv` with a header row. Row count and
truncation are meant to ride on the `X-Sluice-Row-Count` and
`X-Sluice-Truncated` headers instead of the body.

!!! warning "Known issue: CSV metadata headers are not emitted"
    The renderer currently sets `X-Sluice-Row-Count` and
    `X-Sluice-Truncated` after the response body has been committed, so
    HTTP clients never receive them. Use `format: json` when you need the
    row count or the truncation flag.

**Response headers**: `X-Query-Id` always carries the query ID (also present
on errors that have one); `X-Sluice-Applied-Policies` lists the names of
applied policies, comma-separated, when any policy applied.

!!! warning "Not yet implemented: Arrow output"
    `format: arrow` (and `Accept: application/vnd.apache.arrow.stream`) is
    declared in the API but rejected at runtime with
    `ERR_UNSUPPORTED_SYNTAX` ("arrow output not yet supported").

```console
$ curl -s http://localhost:8080/v1/query \
    -H "X-Api-Key: ci-bot.$KEY" -H "Content-Type: application/json" \
    -d '{"sql": "SELECT id, name FROM shop.main.customers", "max_rows": 100}'
```

## Approval endpoints

Registered only when the approval workflow is configured — see
[Approvals](../policies/approvals.md).

### GET|POST /v1/approvals/{id}/accept and /reject

Capability endpoints for human approvers. The token — `?token=` query
parameter or `X-Approval-Token` header — is the sole authorization; no other
authentication applies.

- Unknown ID and bad token return an **identical 404**, so the endpoint
  cannot be used as an existence or token oracle.
- Repeating the same verb is idempotent (`200` with `"note": "already
  decided"`); a conflicting verb after a decision returns `409`.
- **Prefetch-safe**: `HEAD` requests and requests carrying
  `Purpose`/`Sec-Purpose`/`X-Moz: prefetch|preview` headers get `204` and
  never mutate state, so chat clients unfurling the link cannot decide an
  approval.

```console
$ curl -s -X POST "http://localhost:8080/v1/approvals/$ID/accept?token=$TOKEN"
{"approval_id":"…","state":"approved","note":"recorded"}
```

### GET /v1/approvals/{id}

Authenticated status poll for the **requesting subject only** — any other
subject (or an unknown ID) gets `404`. Returns
`{"approval_id": "…", "state": "pending", "expires_at": "…"}`.

## Meta endpoints

| Endpoint | Behavior |
|---|---|
| `GET /v1/health` | Liveness: always `200` with `{"status":"ok","version":"…"}` |
| `GET /v1/ready` | Readiness: `200` when every datasource is healthy; `503` with `"status":"degraded"` otherwise. Lists each datasource as `{name, type, healthy, error}` |
| `GET /v1/version` | Build identity: `{version, commit, build_time, go, parser_version}` |
| `GET /openapi.json` | OpenAPI document (see below) |

!!! warning "OpenAPI document is a stub"
    `/openapi.json` currently returns a minimal OpenAPI 3.1 skeleton that
    lists the paths with no schemas. It exists so clients can
    feature-detect; a generated spec is planned.

## Error envelope

Every 4xx/5xx response from the query and meta endpoints is a JSON
`APIError` (the approval capability endpoints use the simpler shapes shown
above):

```json
{
  "code": "ACL_DENIED",
  "message": "access denied",
  "query_id": "01H…",
  "policy": "deny-finance",
  "details": {}
}
```

`query_id`, `policy`, and `details` are optional. HTTP status is derived
from the code — highlights:

| Status | Codes |
|---|---|
| 202 | `ERR_APPROVAL_PENDING` (query parked, awaiting a human decision) |
| 401 | `ERR_UNAUTHORIZED` |
| 403 | `ACL_DENIED`, `ACL_REJECTED`, `ERR_FORBIDDEN`, `ERR_INSUFFICIENT_SCOPE`, `ERR_APPROVAL_REJECTED` |
| 410 | `ERR_APPROVAL_EXPIRED` |
| 413 | `ERR_PAYLOAD_TOO_LARGE` |
| 429 | `ERR_RATE_LIMITED`, `ERR_BUDGET_EXCEEDED` |

The full 24-code catalogue with canonical messages lives in
[Error codes](error-codes.md).
