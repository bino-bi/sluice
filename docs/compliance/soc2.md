<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# SOC 2

Sluice's mechanisms align with the SOC 2 Trust Services Criteria as
follows. The table is an engineering-level map; a real SOC 2 Type II
attestation requires evidence collection over an observation period,
which is out of scope for this document.

| TSC | Sluice mechanism |
| --- | ---------------- |
| Security (CC1–9) | Default-deny policies; JWT/API-key identity with constant-time verification; hash-chained, fail-closed audit; [approval workflow](../policies/approvals.md) for sensitive reads; per-subject rate limits. |
| Availability (A1) | `/v1/health` and `/v1/ready` probes; global concurrency cap; per-subject rate limits and [daily budgets](../policies/subjects.md); query timeouts and row caps. |
| Confidentiality (C1) | [Column masks](../policies/column-masks.md) (including HMAC hashing and format-preserving encryption); [row filters](../policies/row-filters.md); [classification tags](../policies/classification.md); secrets referenced via `secret://` and never logged. |
| Processing integrity (PI1) | One pipeline for every transport (REST, MCP): parse → policy → rewrite → execute → audit; deterministic conflict resolution (deny wins, mask winner by priority/specificity/name); [policy tests](../policies/testing.md) assert the rewrite output. |
| Privacy (P1–8) | See [GDPR](gdpr.md) — the privacy story is the same across frameworks. |

## Evidence collection

For the observation period, an auditor will typically accept:

- **Audit chain proof** — `sluice audit verify <dir> --json` output
  over the retained audit files.
- **Policy test results in CI** — run `sluice policy test` on every
  policy change and archive the output; a green run is evidence the
  control (filter, mask, deny) behaves as declared.
- **Policy snapshot** — `GET /admin/policies` output plus the git SHA
  of the policy directory at the sample date.
- **Access reviews** — the `SubjectBinding` objects (API key IDs,
  groups, budgets) reviewed on your cadence, straight from git history.
- **Operational metrics** — Prometheus scrape of `GET /metrics` on the
  admin listener; see the [metrics reference](../reference/metrics.md).

## Gaps

!!! warning "Plan compensating controls for these"
    - **Audit forwarding is best-effort** — the built-in syslog and S3
      (Object Lock) sinks replicate records to SIEM/WORM storage, but
      the hash-chained file sink is the sink of record; evidence its
      retention, and treat forwarding gaps as monitored, not impossible
      (`sluice_audit_dropped_total`).
    - **No published signed releases yet** — the pipeline signs with
      cosign and attaches SBOMs, but until a release ships, evidence
      the deployed git commit and your own build process instead.
    - **Single-instance approval broker** — approval decisions are
      audited and pending state survives restarts when
      `approval.persist: true` is set, but the broker is not replicated
      across instances.
    - **No built-in reporting** — Sluice emits raw JSONL and metrics;
      dashboards and periodic control reports are yours to build.
