<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Approvals

An `ApprovalPolicy` holds an allowed query until a person accepts it.

## Example

Goal: a query that names `ssn` or a salary column waits for a data steward.

```yaml
--8<-- "examples/approval-workflow/policies.d/approval.yaml:approval"
```

Alice asks for `ssn`. Sluice holds the query and answers with status 202.

```bash
--8<-- "examples/approval-workflow/steps/01-query-held.sh"
```

```json
--8<-- "examples/approval-workflow/expected/01-query-held.txt"
```

Sluice posts this webhook to each URL in `approval.webhooks`.

```json
--8<-- "examples/approval-workflow/expected/02-webhook.txt"
```

The approver opens `accept_url`.

```json
--8<-- "examples/approval-workflow/expected/03-accept.txt"
```

Alice sends the same query again.

```json
--8<-- "examples/approval-workflow/expected/04-query-runs.txt"
```

**What happened:** The accept created a grant. A grant lets the same subject run the identical SQL text once. The next run is held again.

```mermaid
sequenceDiagram
    autonumber
    participant C as Client
    participant S as Sluice
    participant W as Webhook target
    participant A as Approver
    C->>S: POST /v1/query (matches an ApprovalPolicy)
    S->>W: POST approval payload (accept_url, reject_url)
    S-->>C: 202 ERR_APPROVAL_PENDING + approval_id (after syncWait)
    A->>S: GET or POST accept_url (capability token)
    S-->>A: approved — single-use grant minted
    Note over C,S: MCP clients block on await_approval(approval_id) instead of polling
    C->>S: POST /v1/query (identical SQL, same subject)
    S-->>C: 200 rows (grant consumed)
```

## Fields

| Field | Meaning |
|---|---|
| `when` | Optional. One hit is enough. Without it, the selector alone decides. |
| `when.columnsAccessed` | Patterns for columns that the query names, as `ssn` or as `p.ssn`. |
| `when.predicates[]` | A hit is a comparison in `WHERE`, `HAVING` or `JOIN`. |
| `predicates[].column` | Pattern. Required. |
| `predicates[].op` | `=`, `!=`, `<`, `<=`, `>`, `>=`, `like`, `ilike`, `in` or `isnull`. Empty or `*`: any. |
| `predicates[].value` | The literal as text. Empty: any. |
| `reason` | Text for `reasons` in the webhook. |

With `enforcementMode: Audit`, Sluice holds nothing. `sluice policy explain --json` lists the policy under `shadow`.

## Server config

```yaml
--8<-- "examples/approval-workflow/server.yaml:approval"
```

Sluice does not start with an `ApprovalPolicy` and no `publicBaseUrl`. `syncWait` is at most `rest.requestTimeout` minus 2 seconds.

| Key | Default | Meaning |
|---|---|---|
| `approval.webhooks[].headersRef` | | Secret with a JSON map of headers. |
| `approval.webhooks[].timeout` | 10s | Timeout of one try. |
| `approval.maxPending` | 1000 | Further requests get 429 `ERR_RATE_LIMITED`. |
| `approval.sqlSampleBytes` | 2048 | Longest `sql` in the webhook. 0 sends none. |
| `approval.persist` | `false` | Keeps requests, grants and links in `approval.stateDir`. |

Sluice tries each webhook 3 times. A failed delivery is logged and does not fail the query.

## What the client sees

| Situation | Status | Answer |
|---|---|---|
| No decision within `syncWait` | 202 | `ERR_APPROVAL_PENDING` |
| Accept within `syncWait` | 200 | Rows |
| Reject within `syncWait` | 403 | `ERR_APPROVAL_REJECTED` |
| The request expires within `syncWait` | 410 | `ERR_APPROVAL_EXPIRED` |
| Same query, request still open | 202 | `ERR_APPROVAL_PENDING`, same id, no new webhook |
| Run after a reject or an expired grant | 202 | `ERR_APPROVAL_PENDING`, new id |

The subject that asked reads the state with `GET /v1/approvals/{id}`. Other subjects get 404. States: `pending`, `approved`, `rejected`, `expired`, `consumed`.

```json
--8<-- "examples/approval-workflow/expected/07-status.txt"
```

MCP clients use `check_approval` and `await_approval`. The operator lists open requests with `GET /admin/approvals`. The [audit log](../security/audit.md) records each request, decision and expiry.

## Limits

- `columnsAccessed` sees only column names written in the query. `SELECT *` does not trigger it.
- Without `approval.persist`, a restart drops all requests and grants. A query that waits never survives a restart.
- Run one instance. Instances do not share requests.
- A link needs no login, only its token. A wrong id or token gets 404.
- A second decision with the other verb gets 409. A link preview changes nothing.

## See also

- [Approval workflow](../cookbook/approval-workflow.md)
- [Configuration](../reference/configuration.md)
- [MCP tools](../reference/mcp.md)
- [Admin API](../reference/admin-api.md)
