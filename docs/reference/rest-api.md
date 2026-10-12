<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# REST API

Clients send SQL to Sluice over HTTP. The default address is `:8080`, set by `rest.listen`.

## Routes

| Method | Path | Auth | What it does |
|---|---|---|---|
| `POST` | `/v1/query` | API key or JWT | Runs one SQL statement. |
| `GET` | `/v1/approvals/{id}` | API key or JWT | Returns the state of an approval to its owner. |
| `GET`, `POST` | `/v1/approvals/{id}/accept` | approval token | Accepts the approval. |
| `GET`, `POST` | `/v1/approvals/{id}/reject` | approval token | Rejects the approval. |
| `GET` | `/v1/health` | none | Returns 200 while the server runs. |
| `GET` | `/v1/ready` | none | Returns 503 when a data source is unhealthy. |
| `GET` | `/v1/version` | none | Returns the version of the build. |

Send an API key as `X-Api-Key: <id>.<secret>` or a JWT as `Authorization: Bearer <jwt>`. See [Subjects](../policies/subjects.md).

## Example

A query with `max_rows`:

```bash
--8<-- "examples/rest-api/steps/01-query.sh"
```

```json
--8<-- "examples/rest-api/expected/01-query.txt"
```

The table has 4 rows, so `truncated` is `true`.

## Request fields

| Field | Type | Meaning |
|---|---|---|
| `sql` | string | The SQL statement. |
| `max_rows` | int | Row limit. Default `limits.maxRows`, at most `limits.maxRowsCeiling`. |
| `timeout_ms` | int | Query timeout. Default `limits.queryTimeout`, at most `limits.maxQueryTimeout`. |
| `format` | string | `json` (default) or `csv`. Without it the `Accept` header decides. |
| `meta` | object | String pairs for the audit record (`client_meta`). At most 16 pairs, 64-byte keys, 256-byte values. |

## Response fields

| Field | Type | Meaning |
|---|---|---|
| `query_id` | string | Query id, also in the audit record. |
| `columns` | array | Column names. |
| `rows` | array | One array per row. |
| `row_count` | int | Number of returned rows. |
| `truncated` | bool | `true` when the row limit cut the result. |
| `warning` | object | Set when truncated, with `code` `ERR_RESULT_TRUNCATED`. |

With `format: csv` the body is CSV:

```bash
--8<-- "examples/rest-api/steps/03-csv.sh"
```

```text
--8<-- "examples/rest-api/expected/03-csv.txt"
```

## Response headers

| Header | Set when |
|---|---|
| `X-Request-Id` | Always. Your value, or a new one. |
| `X-Query-Id` | The request got a query id. |
| `X-Sluice-Applied-Policies` | A policy applied. Names, separated by commas. |
| `WWW-Authenticate` | The credential is missing or wrong: 401 `ERR_UNAUTHORIZED`. |
| `X-Sluice-Row-Count`, `X-Sluice-Truncated` | CSV only. HTTP trailers, sent after the body. |
| `X-Sluice-Warning` | The same, when the result is truncated. |

## Errors

An error is a JSON object. [Error codes](error-codes.md) lists every code with its HTTP status.

```bash
--8<-- "examples/rest-api/steps/02-denied.sh"
```

```json
--8<-- "examples/rest-api/expected/02-denied.txt"
```

| Field | Meaning |
|---|---|
| `code` | Error code. |
| `message` | Text for people. |
| `query_id` | Query id, if the request got one. |
| `policy` | Policy that refused the query, if one did. |
| `details` | Extra data, for example `approval_id`. |

## Approval routes

The routes exist when the server config sets `approval.publicBaseUrl`. Send the token as `?token=` or as an `X-Approval-Token` header. See [Approvals](../policies/approvals.md).

| Request | Answer |
|---|---|
| Right token | 200, `note: recorded` |
| Same decision again | 200, `note: already decided` |
| Other decision later | 409 |
| Unknown id or wrong token | 404 |
| `HEAD`, or a header `Purpose: prefetch` | 204, no change |

`GET /v1/approvals/{id}` returns:

```json
--8<-- "examples/approval-workflow/expected/07-status.txt"
```

## Limits

- An unknown body field, `format` or `Accept` value gives 400 `ERR_SYNTAX`.
- A body over `rest.maxBodyBytes` gives 413 `ERR_PAYLOAD_TOO_LARGE`.
- A request longer than `rest.requestTimeout` gives 504 `ERR_TIMEOUT`.
- `limits.globalRps` and `limits.perIpRps` limit `/v1/query` before authentication: 429 `ERR_RATE_LIMITED`.
- A 404 or 405 answer is plain text, not JSON.

## See also

- [Request lifecycle](../architecture/request-lifecycle.md)
- [Deployment](../operations/deployment.md)
