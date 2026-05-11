<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# GDPR

Sluice is a **processor** in GDPR terminology. The controller (you)
decides which personal data flows through which catalog; Sluice
enforces the rules and records what happened.

## Mechanism map

| GDPR principle                  | Sluice mechanism                                                      |
| ------------------------------- | --------------------------------------------------------------------- |
| Lawfulness, fairness, transparency | `sluice policy explain` surfaces the exact effective decision for a subject + table pair. |
| Purpose limitation              | Default-deny; every query requires an explicit `SqlAccessPolicy` allow. |
| Data minimisation               | Column masks (null/constant) redact PII at the AST; row filters restrict to the legitimate subject scope. |
| Accuracy                        | Read-only attach in MVP prevents downstream mutation through Sluice. |
| Storage limitation              | Audit files rotate daily + size cap; retention policy is the operator's responsibility. |
| Integrity + confidentiality     | Hash-chained audit; TLS upstream; secrets never logged (`Redacted{}`). |
| Accountability                  | Every terminal outcome emits exactly one audit record; `sluice audit verify` proves tamper-freedom. |

## Data subject rights

Sluice does not store user data — it enforces access over data owned
by other systems. Rights requests (access, rectification, erasure,
portability) are answered against the underlying source of truth.
Audit records may retain metadata (user ID, query text, timestamps);
align your retention schedule with the DSR timeline.

## DPIAs

The residual risks in the [threat model](../security/threat-model.md)
— CASE oracle, secret TTL, LLM prompt jailbreak — belong in your DPIA
risk register.
