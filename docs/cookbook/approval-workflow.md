<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Approval workflow

**Goal:** reads that touch PII on the `people` table need a human sign-off. The
query is parked, an approver gets a webhook with accept/reject links, and the
identical query executes once — and only once — after acceptance. Runnable
version: `examples/approval-workflow/`.

## The policies

Approvals gate *allowed* queries, so an `ApprovalPolicy` always rides on top of
a `SqlAccessPolicy`:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata: { name: allow-analysts, priority: 0 }
spec:
  effect: allow
  match:
    any:
      - subjects: { groups: [analysts] }
        resources: { tables: ["*"] }
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ApprovalPolicy
metadata: { name: approve-pii-reads, priority: 100 }
spec:
  match:
    any:
      - resources: { tables: [people] }
  when:
    columnsAccessed: ["ssn", "salary*"]
    predicates:
      - { column: "country", op: "=", value: "de" }
  reason: "PII columns on people require a data-steward sign-off"
```

The `when` conditions are OR'd: touching `ssn` or any `salary*` column, **or**
filtering on `country = 'de'`, triggers the approval. An empty `when` makes the
selector alone trigger it.

## Server configuration

The example README shows the `approval` block to add to your `server.yaml`:

```yaml
# fragment — approval section of server.yaml
approval:
  publicBaseUrl: https://sluice.example.com   # builds the capability URLs
  syncWait: 20s                               # in-request wait before the 202
  requestTtl: 15m                             # pending request lifetime
  grantTtl: 5m                                # window to re-run after accept
  webhooks:
    - url: https://hooks.example.com/sluice
      headersRef: secret://env/APPROVAL_WEBHOOK_HEADERS  # JSON header map
```

`publicBaseUrl` is **required** once any `ApprovalPolicy` loads — Sluice
refuses to start without it (fail-closed).

## Walkthrough

`examples/approval-workflow/run.sh` drives the flow. Point a webhook catcher at
the configured `webhooks[].url`, then:

1. **Submit** — `run.sh` POSTs `SELECT ssn FROM shop.main.people WHERE country
   = 'de'` to `/v1/query`. If no approver acts within `syncWait`, the response
   is **HTTP 202** with code `ERR_APPROVAL_PENDING` and an `approval_id`.
2. **Webhook** — every configured target receives:

    ```json
    {
      "approval_id": "01JZX…",
      "subject": { "id": "alice", "issuer": "", "email": "", "groups": ["analysts"] },
      "sql": "SELECT ssn FROM shop.main.people WHERE country = 'de'",
      "reasons": ["policy approve-pii-reads: PII columns on people require a data-steward sign-off"],
      "policies": ["approve-pii-reads"],
      "accept_url": "https://sluice.example.com/v1/approvals/01JZX…/accept?token=…",
      "reject_url": "https://sluice.example.com/v1/approvals/01JZX…/reject?token=…",
      "requested_at": "2026-07-06T10:00:00Z",
      "expires_at": "2026-07-06T10:15:00Z"
    }
    ```

3. **Accept** — `run.sh` prompts for the `accept_url` from your catcher and
   curls it. The unguessable token *is* the authorisation — no approver login.
4. **Re-run** — the **identical** query (same subject, same raw SQL) now
   executes and returns rows. The grant is single-use and consumed.
5. **Re-run again** — the same query pends again (`ERR_APPROVAL_PENDING`):
   step 4 consumed the grant.

Decisions are idempotent per verb; sending the opposite verb to a decided
request returns 409. A rejected query returns `ERR_APPROVAL_REJECTED` (403);
an expired request returns `ERR_APPROVAL_EXPIRED` (410).

## Agent variant

MCP agents do not need to poll manually: after `execute_sql` returns
`ERR_APPROVAL_PENDING` with an `approval_id`, the agent calls
`await_approval { approval_id, timeout_seconds }` (max 55 s per call) to block
until a decision lands, then re-issues the identical query. `check_approval`
gives a non-blocking status snapshot. See [MCP agents](mcp-agents.md).

## Pitfalls

- **`publicBaseUrl` reachability.** The accept/reject URLs are built from this
  value. If approvers cannot reach it (internal hostname, wrong scheme, no
  TLS), the capability links in the webhook are dead and every request expires.
- **Webhook receiver availability.** Delivery is retried 3 times with
  exponential backoff; if all attempts fail the request stays pending until
  `requestTtl` — silently, from the approver's point of view. Monitor your
  receiver.
- **`grantTtl` expiry.** The grant window (default 5m) starts at acceptance.
  A requester who re-runs too late is parked again and needs a fresh approval.
- **State is in-memory.** A restart drops pending requests and grants; callers
  re-submit. This is a single-instance feature — do not load-balance approvals
  across replicas.

## See also

- [Approvals](../policies/approvals.md) — `when` semantics, broker states, REST endpoints.
- [Error codes](../reference/error-codes.md) — `ERR_APPROVAL_*` reference.
