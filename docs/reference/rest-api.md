<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# REST API

Clients send SQL to Sluice over HTTP. The default address is `:8080`, set by `rest.listen`.

## Routes

| Method | Path | Auth | What it does |
|---|---|---|---|
| `POST` | `/v1/query` | API key or JWT | Runs one SQL statement. |
| `GET` | `/v1/approvals/{id}` | API key or JWT | Returns the state of an approval to its owner. |
| `POST` | `/v1/approvals/{id}/accept` | approval token | Accepts the approval. A `GET` returns an HTML page with a button that sends the `POST`. |
| `POST` | `/v1/approvals/{id}/reject` | approval token | Rejects the approval. A `GET` returns such a page too. |
| `GET` | `/v1/health` | none | Returns 200 with `status` and `version` while the server runs. |
| `GET` | `/v1/ready` | none | Returns 503 when a data source is unhealthy. |
| `GET` | `/v1/version` | none | Returns `version`, `commit`, `build_time`, `go` and `parser_version` of the build. |

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
| `format` | string | `json` (default) or `csv`. Without it the `Accept` header decides: `text/csv` gives CSV, `application/json` or `*/*` gives JSON. |
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

A 404 for an unknown path and a 405 are plain text. The approval routes answer 404 as `{"error": "not found"}`.

## Approval routes

The routes exist when the server config sets `approval.publicBaseUrl`. Send the token as `?token=` or as an `X-Approval-Token` header. The table shows the answers to a `POST`, and to a `GET` in the last row. See [Approvals](../policies/approvals.md).

| Request | Answer |
|---|---|
| Right token | 200, `note: recorded` |
| Same decision again | 200, `note: already decided` |
| Other decision later | 409. After the query ran: 200, state `consumed` |
| Unknown id or wrong token | 404 |
| `GET` or `HEAD`, any id and token | 200, the same HTML page, no change |

`GET /v1/approvals/{id}` returns the state:

```bash
--8<-- "examples/approval-workflow/steps/07-status.sh"
```

```json
--8<-- "examples/approval-workflow/expected/07-status.txt"
```

## Limits

- A body field outside the Request fields table gives 400 `ERR_SYNTAX`, except `params`: Sluice ignores an array there.
- A statement with a parameter such as `$1` gives 400 `ERR_UNSUPPORTED_SYNTAX`. [Request lifecycle](../architecture/request-lifecycle.md) lists the statement rules.
- An unknown `format` or `Accept` value gives 400 `ERR_SYNTAX`. A request for Arrow output gives 400 `ERR_UNSUPPORTED_SYNTAX`.
- A body over `rest.maxBodyBytes` gives 413 `ERR_PAYLOAD_TOO_LARGE`. A request longer than `rest.requestTimeout` gives 504 `ERR_TIMEOUT`.
- `limits.globalRps` and `limits.perIpRps` limit `/v1/query` before authentication: 429 `ERR_RATE_LIMITED`.

## See also

- [Request lifecycle](../architecture/request-lifecycle.md)
- [Deployment](../operations/deployment.md)
