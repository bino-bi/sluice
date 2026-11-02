<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# REST API

Clients send SQL to Sluice over HTTP. The default address is `:8080`, set by `rest.listen`.

## Routes

| Method | Path | Auth | What it does |
|---|---|---|---|
| `POST` | `/v1/query` | API key or JWT | Runs one SQL statement. |
| `POST` | `/v1/sample` | API key or JWT | Returns a random sample of rows from one table. |
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
| `truncated` | bool | `true` when the row limit or `limits.maxResultBytes` cut the result. |
| `warning` | object | Set when truncated, with `code` `ERR_RESULT_TRUNCATED`. |

With `format: csv` the body is CSV:

```bash
--8<-- "examples/rest-api/steps/03-csv.sh"
```

```text
--8<-- "examples/rest-api/expected/03-csv.txt"
```

## Sample request fields

`POST /v1/sample` returns a random sample of one table. Sluice builds a `SELECT` from the request. The query then passes the same policies, limits, budgets and approvals as `POST /v1/query`. Row filters run before the sample, so the sample holds only rows the subject may see.

```bash
--8<-- "examples/rest-api/steps/04-sample.sh"
```

```json
--8<-- "examples/rest-api/expected/04-sample.txt"
```

The response has the same fields and headers as for `POST /v1/query`.

| Field | Type | Meaning |
|---|---|---|
| `table` | string | Required. A name of the form `catalog.schema.table`. |
| `columns` | array | Column names to return. Empty returns all columns. |
| `rows` | int | Number of rows. Set `rows` or `rate`, not both. |
| `rate` | number | Share of rows, from `0.000001` to `1`. |
| `method` | string | See the table below. Default `reservoir` with `rows`, `bernoulli` with `rate`. |
| `stratify_by` | array | Column names whose value mix the sample keeps. Only with `stratified`. |
| `seed` | int | From `0` to `2147483647`. Without it Sluice picks a random seed. |
| `where` | string | A SQL condition on the table. Policies check it like the SQL of a query. |
| `format` | string | `json` (default) or `csv`, as for `POST /v1/query`. |

| Method | Size | What it does |
|---|---|---|
| `reservoir` | `rows` | Returns exactly `rows` rows, or all rows of a smaller table. |
| `bernoulli` | `rate` | Keeps each row with the chance `rate`. The row count varies. |
| `system` | `rate` | Keeps whole blocks of rows. It is fast but less random. On a small table it can return no rows. |
| `stratified` | `rows` or `rate` | Gives each group of `stratify_by` its share of the sample. |

- `rows` above the cap is lowered to the cap without an error. The cap is the smallest of `sample.maxRows`, `limits.maxRows` and `limits.maxRowsCeiling` minus one.
- A `rate` sample has no cap of its own. `limits.maxRows` cuts the result. A cut sample holds the first sampled rows in scan order, so it is not uniform.
- `stratified` drops groups with fewer rows than `sample.minGroupRows`. With `rate`, it keeps at least one row of each other group, unless `limits.maxRows` cuts the result. With `rows`, it can drop whole groups when there are more groups than `rows`.
- `stratified` is refused with `ERR_MASK_UNSUPPORTED_CONTEXT` when a post-query mask applies to a returned column. Leave that column out of `columns`.
- With `stratified`, a table with a column named `sluice_h`, `sluice_rn`, `sluice_n`, `sluice_t` or `sluice_x` needs explicit `columns`.
- With the same seed, data and policies, `reservoir` and `stratified` return the same rows.
- `where` must stay one condition. A text that adds a `UNION` or another clause gives `ERR_UNSUPPORTED_SYNTAX`.
- A wrong field value gives 400 `ERR_SYNTAX`. Names of up to 63 bytes are allowed. Names that start with `sluice_` are reserved.
- The audit record carries `extras.action: sample` and the request in `extras.sample`. See [Audit log](../security/audit.md).

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
- `limits.globalRps` and `limits.perIpRps` limit `/v1/query` and `/v1/sample` before authentication: 429 `ERR_RATE_LIMITED`.

## See also

- [Request lifecycle](../architecture/request-lifecycle.md)
- [Deployment](../operations/deployment.md)
