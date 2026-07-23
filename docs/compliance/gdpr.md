<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# GDPR

Sluice is a **processor** in GDPR terminology. The controller (you)
decides which personal data flows through which catalog; Sluice
enforces the rules and records what happened.

## Mechanism map

| GDPR principle | Sluice mechanism |
| -------------- | ---------------- |
| Lawfulness, fairness, transparency | `sluice policy explain` and the MCP `explain_access` tool surface the exact effective decision for a subject + table pair. |
| Purpose limitation | Default-deny: every query requires an explicit `SqlAccessPolicy` allow, and [row filters](../policies/row-filters.md) narrow allowed tables to the legitimate scope (tenant, region, ownership). |
| Data minimization | [Column masks](../policies/column-masks.md) redact or pseudonymize PII in-flight (`null`, `partial`, `hash`, `fpe`, `fake`, …); [classification tags](../policies/classification.md) let you label PII columns once and mask them everywhere by tag. |
| Storage limitation | Audit files rotate daily and by size; the retention schedule for rotated files is the operator's responsibility. |
| Security of processing (Art. 32) | Default-deny — an empty policy directory means "deny everything"; the statement allowlist (SELECT, EXPLAIN, SET, SHOW, PRAGMA) rejects all writes; secret values are handled via `secret://` references and never appear in logs, errors, or audit records. |
| Accountability | Every terminal outcome appends a [hash-chained audit](../security/audit.md) record; `sluice audit verify` proves the chain is intact. Policies are plain YAML in git, so who changed which rule when is your normal review history. |
| Human oversight of sensitive access | An [ApprovalPolicy](../policies/approvals.md) holds queries that touch designated columns or predicates until a human accepts, and the decision is audited. |

## Data subject rights

Sluice does not store the data it brokers — it enforces access over
data owned by other systems. Rights requests (access, rectification,
erasure, portability) are answered against the underlying source of
truth. Sluice's own state can still contain personal metadata: audit
records may retain subject ID, email, SQL sample, and timestamps, and
when budgets are enabled the budget state directory keeps per-subject
daily usage counters (subject ID and issuer) for `retentionDays`
(default 35). Align both retention schedules with your DSR timeline.

## DPIAs

Include the residual risks documented in the
[threat model](../security/threat-model.md) in your DPIA risk
register, together with the gaps below.

## Gaps

!!! warning "What Sluice does not do"
    - **`postgres` and `otlp` audit sinks are not implemented.** Syslog
      forwarding and S3 archival (optionally under Object Lock) are
      built in via the `audit.*` server config; they are best-effort —
      long-term retention guarantees rest on the hash-chained file sink
      and your handling of its files.
    - **No built-in DSR tooling.** Sluice has no "find/export/erase all
      data about person X" function — neither for the upstream databases
      nor for its own audit files.
    - **Pseudonymization keys are yours to manage.** `hash` salts and
      `fpe` keys come from `secret://` references; rotation and escrow
      are covered in [key rotation](../security/key-rotation.md) but
      executed by you.
