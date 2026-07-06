<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Compliance

Sluice's data-plane behavior maps onto controls from three common
frameworks. Use these pages as a starting point for your auditor's
questionnaire; they are not a substitute for a formal attestation.
Sluice *supports* controls — it never satisfies a framework by itself.

- [GDPR](gdpr.md) — data minimization, purpose limitation,
  accountability, human oversight.
- [BSI C5](bsi-c5.md) — German cloud computing compliance controls.
- [SOC 2](soc2.md) — Trust Services Criteria (security, availability,
  confidentiality, processing integrity).

## The mechanism toolbox

Each framework page maps controls onto the same set of enforcement
mechanisms:

| Mechanism | What it gives an auditor |
| --------- | ------------------------ |
| Default-deny access policies | Every readable table traces to an explicit, git-reviewable `SqlAccessPolicy`. |
| [Row filters](../policies/row-filters.md) | Per-subject scoping (tenant, region, ownership) enforced in the rewritten SQL. |
| [Column masks](../policies/column-masks.md) | PII redaction: `null`, `constant`, `partial`, `hash`, `regex`, `truncate` in-query; `hash` (HMAC), `fpe`, `jitter`, `fake` post-query. |
| [Classification tags](../policies/classification.md) | Label columns (`pii.email`, …) once, target policies by tag instead of by name. |
| [Approvals](../policies/approvals.md) | Human sign-off before a sensitive query executes. |
| [Budgets and rate limits](../policies/subjects.md) | Per-subject daily row/CPU quotas and request rates. |
| [Hash-chained audit](../security/audit.md) | Tamper-evident JSONL record of every decision; `sluice audit verify` proves integrity. |
| [Policy testing](../policies/testing.md) | `sluice policy test` turns your control expectations into CI-checkable assertions. |

Each framework page ends with a gaps section listing what Sluice does
**not** provide, so you can plan compensating controls instead of
discovering the gap during the audit.
