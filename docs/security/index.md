<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Security

Sluice stands between clients you do not trust and your data. This page lists the defaults and the fail-closed cases.

## Defaults

| Area | Default |
|---|---|
| Access | Default deny. A query runs only when policies allow every table it reads. |
| Statements | Read-only. Sluice rejects writes and attaches databases `READ_ONLY`. |
| [Audit log](audit.md) | `audit.failClosed: true`. A client gets rows only after Sluice queued the audit record. |
| Admin API | Off. Without `admin.token` it accepts every request. |
| TLS | Off. REST and admin serve plain HTTP until you set `tls`. MCP over HTTP has no `tls` setting. |
| Secrets | Sluice writes no secret value to logs, metrics or the audit log. |

See the [hardening checklist](hardening.md) and the [threat model](threat-model.md).

## When Sluice fails closed

In each case the client gets an error and no rows.

| Case | Error code |
|---|---|
| No policy allows a table | `ACL_DENIED` |
| A CEL condition fails with an error | `ACL_DENIED` |
| OPA returns no decision | `ACL_DENIED` |
| OPA returns an unknown field, or OpenFGA returns an error | `ERR_INTERNAL` |
| A policy template uses a claim the subject lacks | `ERR_INTERNAL` |
| The audit queue does not take the record | `ERR_AUDIT_UNAVAILABLE` |
| The approval queue is full | `ERR_RATE_LIMITED` |

With budgets on and `budget.failClosed: true`, Sluice does not start when the budget store cannot open.

## Report a vulnerability

Follow [`SECURITY.md`](https://github.com/bino-bi/sluice/blob/main/SECURITY.md) in the repository.
