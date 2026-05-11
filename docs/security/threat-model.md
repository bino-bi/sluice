<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Threat model

The full STRIDE document lives at
[`THREAT_MODEL.md`](https://github.com/bino-bi/sluice/blob/main/THREAT_MODEL.md)
in the repo root (≈3.6 k words, reviewed per release). This page
summarises the shape of the document and the residual risks called out
by it.

## Trust boundaries

1. **Client → Sluice** (REST / MCP / admin). Mitigations: TLS termination
   upstream or stunnel-style sidecar; mutual TLS on admin port (v0.3);
   constant-time compare on admin static token; rate limiting (v0.2).
2. **Sluice → Backend** (Postgres, MySQL, S3, DuckDB file, MotherDuck).
   Mitigations: empty ATTACH URL + named secret so credentials never
   land in DuckDB query logs; `READ_ONLY` on every attach in MVP;
   `allowedPaths` glob whitelist for S3 Parquet.
3. **Sluice → Filesystem** (audit, config, secrets). Mitigations: file
   permission checks (0o022 rejected; 0o004 warned); append-only audit
   writes; hash-chained records; distroless non-root runtime.
4. **Sluice → IDP** (JWKS fetch). Mitigations: stale-serve on transient
   failure; rate-limited unknown-kid refresh (30 s floor); algorithm
   allow-list excludes `alg: none`.

## Residual risks

Called out in the root threat model:

- **CASE-expression oracle in column masks** — a user could in theory
  craft a WHERE that discriminates the masked vs real column. The
  rewriter substitutes at every reference site, mitigating but not
  eliminating this. A full-query rewrite model (plan 15 §future) closes
  the gap; MVP accepts the residual risk.
- **Time-of-check vs time-of-use on secrets** — cached secret values
  honour per-entry TTLs; rotating a secret mid-window leaves the old
  value in use until TTL expiry.
- **LLM jailbreak via SQL** — the rewriter blocks DML/DDL on any
  attached catalog, so even a prompt-jailbroken agent cannot write
  through Sluice.

## Reporting

Any finding not covered above should be reported via the process in
`SECURITY.md`.
