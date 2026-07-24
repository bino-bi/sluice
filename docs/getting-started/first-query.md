<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# First query

With the [quickstart](quickstart.md) running, this page dissects the request you sent, tightens the email mask, and shows the tooling that explains every decision.

## Anatomy of POST /v1/query

Request body:

| Field | Type | Meaning |
| --- | --- | --- |
| `sql` | string | The statement. Exactly one; write statements are rejected. |
| `params` | array | Positional parameters for `$1`, `$2`, … in your SQL. |
| `max_rows` | int | Row cap for this request; clamped to the server limit. |
| `timeout_ms` | int | Per-request timeout; clamped to the server maximum. |
| `format` | string | `json` (default) or `csv`. |
| `meta` | object | Free-form key/values, recorded on the audit access record as `client_meta` (size-capped). |

Response body (`format: json`): `query_id` (the ULID of this request's audit records), `columns`, `rows`, `row_count`, `truncated`. Two headers accompany every response: `X-Query-Id` and `X-Sluice-Applied-Policies`, a comma-separated list of the policies that shaped the result.

!!! warning "Not yet implemented: Arrow"
    The API declares `format: arrow`, but the server rejects it with `ERR_UNSUPPORTED_SYNTAX` (`arrow output not yet supported`). Use `json` or `csv`.

## The row filter you already have

The quickstart directory ships `policies.d/filter-tenant.yaml`:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: RowFilterPolicy
metadata:
  name: filter-tenant
  priority: 80
spec:
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["customers", "orders"]
  combine: restrictive
  filter:
    predicate:
      column: tenant_id
      op: Equals
      value: "{{ subject.tenantId }}"
```

Sluice rewrites each matching table into a filtered subquery — `... FROM (SELECT * FROM shop.main.customers WHERE tenant_id = $1) customers` — binding the caller's `tenantId` as a parameter. That is why only the two `acme` rows ever leave the gateway.

## Sharpen the mask: from null to partial

The stock mask nulls `email` entirely. Replace the contents of `policies.d/mask-email.yaml` with a `partial` mask that keeps the first character:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata:
  name: mask-email
  priority: 50
spec:
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["customers"]
          columns: ["email"]
  mask:
    type: partial
    args: { showFirst: 1, showLast: 0 }
```

Hot reload picks the change up on save — no restart. Re-run the same query:

```bash
curl -s \
  -H "X-Api-Key: sl_demo_hello.world" \
  -H "Content-Type: application/json" \
  -d '{"sql":"SELECT id, email, tenant_id FROM shop.main.customers ORDER BY id"}' \
  http://localhost:8080/v1/query
```

```json
{
  "query_id": "01KWV0FD2DK4TRGZG9XCDJKSS1",
  "columns": ["id", "email", "tenant_id"],
  "rows": [
    [1, "a**************", "acme"],
    [2, "b************", "acme"]
  ],
  "row_count": 2,
  "truncated": false
}
```

Still filtered, now partially masked — and `X-Sluice-Applied-Policies: allow-analytics,filter-tenant,mask-email`.

## Why did that happen? `sluice policy explain`

`policy explain` answers "what would this subject get on this table?" without sending a query:

```bash
sluice policy explain --policies-dir examples/hello-sluice/policies.d \
  --user hello --groups analytics --claims tenantId=acme \
  --table shop.main.customers
```

```
subject : hello
resource: shop.main.customers
decision: allow
Kind              Name             Priority  Effect
----              ----             --------  ------
SqlAccessPolicy   allow-analytics  100       applied
RowFilterPolicy   filter-tenant    80        applied
ColumnMaskPolicy  mask-email       50        applied
row filters:      shop.main.customers
column mask:      shop.main.customers.email  -  partial (via mask-email)
```

Add `--json` for machine-readable output.

## Trial a policy with `enforcementMode: Audit`

Every enforcement policy defaults to `enforcementMode: Enforce`. Set it to `Audit` to keep the policy loaded and matched while suspending its effect — useful for trialing a new mask without breaking dashboards:

```yaml
# fragment — add to the spec of mask-email
enforcementMode: Audit
```

Re-run the query: emails come back in the clear, and `mask-email` disappears from `X-Sluice-Applied-Policies` (and from `policies_applied` in the audit records). The query itself is still fully audited — every request lands in the hash-chained log regardless of mode:

```bash
sluice audit verify examples/hello-sluice/data/audit
```

Delete the line (or set `Enforce`) to turn the mask back on.

## For agents

AI agents connected over MCP get the same introspection as `policy explain` through the `explain_access` tool, plus `execute_sql`, `whoami`, and six more — see the [MCP tools reference](../reference/mcp.md).

## Next

- [Row filters](../policies/row-filters.md) — predicates, CEL expressions, and how filters combine.
- [REST API reference](../reference/rest-api.md) — the full request/response contract.
- [Error codes](../reference/error-codes.md) — every `code` the API can return.
