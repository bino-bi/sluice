<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# Approval workflow

This example shows the human-approval flow: a query that reads PII columns
is held, a webhook fires with accept/reject capability URLs, and the
identical query executes once an approver accepts.

## How it works

1. An `ApprovalPolicy` marks queries that touch `ssn` / `salary*` (or filter
   `country = 'de'`) on the `people` table as requiring approval.
2. On such a query, Sluice returns `ERR_APPROVAL_PENDING` (HTTP 202) with an
   `approval_id` and fires a webhook to every configured target.
3. The webhook payload carries `accept_url` and `reject_url` — capability
   URLs whose unguessable token is the sole authorisation (no approver
   login). An approver opens (or POSTs) the accept URL.
4. The requester re-runs the **identical** query (same subject + same raw
   SQL) within the grant TTL; it now executes. The grant is single-use.

State is in-memory: a restart drops pending requests and grants (callers
just re-submit). This is a single-instance feature.

## Configuration

Unlike the other examples, this directory is not a self-contained
stack: it ships only the approval policy (`policies.d/approval.yaml`)
and a driver script (`run.sh`). To try it you need a running
`sluice serve` with:

- the `approval` block below added to your server config,
- a `DataSource` exposing a `shop.main.people` table with `ssn`,
  `salary*`, and `country` columns,
- a `SubjectBinding` for the caller — `run.sh` presents the API key
  `sl_demo_alice.world` and expects the subject in the `analysts`
  group,
- this example's `policies.d/approval.yaml` merged into your policy
  directory,
- a webhook receiver reachable under `approval.webhooks[].url`.

The `examples/hello-sluice` stack is a good base to graft this onto.

```yaml
# fragment — approval block to add to your server.yaml
approval:
  publicBaseUrl: https://sluice.example.com   # builds the capability URLs
  syncWait: 20s                                # in-request wait before 202
  requestTtl: 15m
  grantTtl: 5m
  webhooks:
    - url: https://hooks.example.com/sluice
      headersRef: secret://env/APPROVAL_WEBHOOK_HEADERS   # JSON {"Authorization":"Bearer …"}
```

`approval.publicBaseUrl` is **required** whenever any `ApprovalPolicy` is
loaded — Sluice refuses to start without it (fail-closed).

## Try it

`run.sh` drives the flow against a running Sluice instance (see the
prerequisites above). It:

1. submits a PII query and shows the `202 ERR_APPROVAL_PENDING`,
2. prompts you to paste the `accept_url` delivered to your webhook
   receiver (the script does not run a catcher itself),
3. curls the pasted accept URL as the approver,
4. re-submits the identical query, which now executes,
5. re-submits once more and shows it pends again (grant consumed).

It authenticates with `Authorization: ApiKey sl_demo_alice.world`;
override via `SLUICE_API_KEY` and `SLUICE_BASE`.

```bash
./run.sh
```

## Webhook payload

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

## Security notes

- Accept/reject URLs are **public** (the capability token is the auth) and
  are prefetch-hardened: HEAD and `Purpose: prefetch` requests never mutate
  state, and unknown-id vs bad-token both return an identical 404 (no
  oracle). Chat clients that auto-unfurl links can still be a risk — prefer
  POST-capable approver integrations for anything sensitive.
- The status poll `GET /v1/approvals/{id}` is authenticated and only the
  requesting subject may read it.
