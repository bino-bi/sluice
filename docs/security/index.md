<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Security

Sluice sits between untrusted SQL clients — AI agents, BI tools — and your data.
Its posture rests on five properties:

- **Default-deny.** An empty policy directory is a valid configuration that
  rejects every query. Access exists only where a `SqlAccessPolicy` explicitly
  allows it.
- **Read-only execution.** Only `SELECT`, `EXPLAIN`, `SET`, `SHOW`, and
  `PRAGMA` reach the engine. Writes are rejected before execution, and every
  data source is attached `READ_ONLY`.
- **Fail-closed audit.** By default a query is not served unless its access
  record has been durably enqueued to the hash-chained audit log first.
- **Separate admin plane.** Operational endpoints (`/admin/*`, `/metrics`)
  live on their own listener (`:9091`) behind a static bearer token, intended
  for private networks only.
- **Secret hygiene.** Credentials are referenced via `secret://` URIs, never
  inlined; secret byte values never appear in logs, metrics, or audit records.

## In this chapter

| Page | What it covers |
|---|---|
| [Audit trail](audit.md) | Record anatomy, the hash chain, fail-closed serving, verification, retention |
| [Threat model](threat-model.md) | Assets, adversaries, trust boundaries, mitigations, residual risks |
| [Hardening](hardening.md) | Production checklist: process, network, credentials, audit, data sources, policies |
| [Key rotation](key-rotation.md) | Rotating peppers, API keys, JWT keys, the audit genesis seed, and mask salts |

## Related reading

- [Security model](../architecture/security-model.md) explains the design
  rationale behind these properties — why the pipeline is shaped the way it is.
- The full STRIDE analysis lives in
  [`THREAT_MODEL.md`](https://github.com/bino-bi/sluice/blob/main/THREAT_MODEL.md)
  at the repository root.
- To report a vulnerability, follow
  [`SECURITY.md`](https://github.com/bino-bi/sluice/blob/main/SECURITY.md) —
  private GitHub reporting or `security@bino.bi`, with a 72-hour
  acknowledgement target.
