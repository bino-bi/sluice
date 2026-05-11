<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# SOC 2

Sluice's mechanisms align with the SOC 2 Trust Services Criteria as
follows. The table is an engineering-level map; a real SOC 2 Type II
attestation requires evidence collection over an observation period,
which is out of scope for this document.

| TSC              | Sluice mechanism                                                                 |
| ---------------- | -------------------------------------------------------------------------------- |
| Security (CC1–9) | Default-deny policy; identity middleware; hash-chained audit; SBOM + signed release artefacts; cosign keyless verification for supply-chain integrity. |
| Availability (A1) | Graceful shutdown (10 s drain); health + readiness probes; configurable executor pool; backpressure metrics on audit dispatcher. |
| Confidentiality (C1) | Column masks applied at AST rewrite; secrets never logged; `Redacted{}` wrapper in telemetry; TLS termination upstream. |
| Processing integrity (PI1) | Every query runs through the same `queryservice.Service`; rewrites are deterministic (priority desc → specificity desc → name asc); fingerprinted via pg_query. |
| Privacy (P1–8)   | See [GDPR](gdpr.md) — the privacy story is the same across frameworks.           |

## Evidence collection

The auditor will typically want:

- **Audit samples** with chain verification output.
- **Policy directory** at a given point in time (git sha).
- **Reload logs** showing `sluice_config_reloads_total` increments.
- **SBOM + signature** for the deployed binary.
- **Access reviews** of `SubjectBinding` objects and issued API keys.

All five are producible via the CLI and the metrics endpoint.
