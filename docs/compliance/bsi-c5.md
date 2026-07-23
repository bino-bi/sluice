<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# BSI C5

The BSI Cloud Computing Compliance Criteria Catalogue (C5:2020) covers
17 objective areas. Sluice's relevant mechanisms cluster around
**IDM** (identity), **KRY** (cryptography), **BCM** (continuity),
**SIM** (incident management), and **OPS** (operations).

| Area | Control (sample) | Sluice mechanism |
| ---- | ---------------- | ---------------- |
| IDM-01 | Identity lifecycle | [SubjectBinding](../policies/subjects.md) objects tie JWT issuers and API keys to canonical subjects, groups, rate limits, and budgets — all reviewable YAML in git. |
| IDM-09 | Privileged access monitoring | The admin API sits behind a static bearer token (constant-time compare). Admin policy-explain calls are audited as `admin-action` events; config reloads and the other admin endpoints are not currently audited — cover them with infrastructure/access logs. Break-glass elevation is a selector exclude, so every elevated query is a normal, reviewable audit record. |
| KRY-01 | Cryptographic usage guidelines | JWT algorithm allowlist (HS256/384, RS256/384, ES256/384; `alg=none` rejected); API keys verified with constant-time HMAC-SHA256 over a peppered hash. |
| KRY-04 | Key rotation | Pepper, JWKS, mask salts/keys, and the audit genesis anchor are rotated per [key rotation](../security/key-rotation.md). |
| BCM-03 | Audit-log continuity | Hash-chained JSONL; `sluice audit verify` detects tampering or gaps (exit code 4). Audit is fail-closed by default: if a record cannot be enqueued durably, the query does not run. |
| SIM-03 | Evidence preservation | Append-only audit files, daily plus size-based rotation, audit directory created `0750`; rotation preserves prior files. |
| OPS-04 | Configuration management | Policies live in a git-tracked directory; reloads are validate-then-swap atomic (a bad reload keeps the prior snapshot); `sluice policy validate` and `sluice policy test` gate changes in CI. |
| OPS-09 | Vulnerability management | `govulncheck`, `gosec`, and `trivy` lanes run in CI; CodeQL runs weekly. |
| — | Gaps | See below — file-only audit sink, single-instance approvals, no published release artifacts yet. |

For a full attestation, engage a BSI-qualified auditor. This page is
an engineering-level map, not evidence.

## Gaps

!!! warning "What Sluice does not provide today"
    - **`postgres` and `otlp` audit sinks are not implemented.** Built-in
      forwarding covers syslog (SIEM hand-off) and S3 with Object Lock
      (WORM archival) via the `audit.*` server config; both are
      best-effort secondaries — the hash-chained file sink remains the
      durable record, so evidence the file retention path.
    - **No published release artifacts.** The release pipeline is set
      up to sign binaries with cosign and attach CycloneDX SBOMs, but
      no release has been published yet — build from source and record
      the git commit you deployed.
    - **Single-instance approval broker.** Approval state is not
      replicated across instances. Durability across restarts is opt-in
      (`approval.persist: true`, SQLite-backed); without it a restart
      drops pending requests.
    - **No OTel tracing.** Observability is structured logs plus
      Prometheus metrics on the admin listener; see
      [observability](../operations/observability.md).
