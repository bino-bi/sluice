<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# Threat Model

This document is the single source of truth for how Sluice reasons about adversaries. The public docs site links here; `SECURITY.md` covers the disclosure process. Updates follow the governance process (RFC for material changes, PR review for clarifications).

---

## 1. Scope

Sluice is a SQL access-control gateway. Clients send SQL; Sluice parses it, evaluates a declarative policy against the caller's identity, rewrites the statement to enforce the policy, executes it against DuckDB-attached data sources, and emits a hash-chained audit record of the outcome.

**In scope** for this threat model:

- Sluice's own code under `cmd/`, `internal/`, `pkg/`.
- The YAML configuration surface (`ServerConfig` + policy directory).
- The three transports: REST (`/v1/...`), MCP (stdio + Streamable HTTP), admin (`/admin/...`).
- Audit durability and integrity.
- Secret handling (`secret://` resolution, API-key pepper, JWKS keys, driver credentials).
- Interactions with DuckDB and the six first-party data source drivers.

**Out of scope** (owned by the operator or upstream projects):

- The security of the upstream databases themselves. Sluice assumes Postgres, MySQL, and the S3 bucket each enforce their own access controls and rotate their own credentials.
- Physical and network security of the host running Sluice.
- The browser-side security of BI tools talking to Sluice.
- CVEs in transitive dependencies that do not have an exploitable path through Sluice code — we monitor `govulncheck` and patch, but the dependency project owns the fix.

## 2. Assets and trust boundaries

| Asset                          | Why it matters                                           | Owner              |
|--------------------------------|----------------------------------------------------------|--------------------|
| Query results                  | May contain PII, financial data, anything in the DBs.    | Sluice + upstream  |
| Policy snapshot                | Defines who sees what; tamper == bypass.                 | Sluice             |
| Identity material              | JWT signing keys, API-key pepper, HMAC secrets.          | Sluice + KMS       |
| Driver credentials             | Database passwords, S3 keys, MotherDuck tokens.          | Sluice + KMS       |
| Audit log                      | Evidence of every query; downstream compliance depends.  | Sluice + WORM      |
| Audit genesis seed             | Anchors the hash chain; losing it enables silent resets. | Sluice + KMS       |
| Admin token                    | Grants read access to policies + subject explain.        | Sluice             |

Trust boundaries (outermost → innermost):

1. **Public network** (internet or tenant network) ↔ **Sluice REST/MCP listener**.
2. **Sluice process** ↔ **DuckDB embedded engine** (in-process, but hardened).
3. **Sluice process** ↔ **upstream databases** (network, TLS).
4. **Sluice process** ↔ **local filesystem** (config, audit, secrets via `file://`).
5. **Operator console** ↔ **admin listener** (typically private network only).

## 3. Data flow (summary)

```
client ──REST/MCP──▶ identity ──▶ queryservice
                                     │
                                     ├─▶ parser   (pg_query AST)
                                     ├─▶ policy   (atomic snapshot)
                                     ├─▶ rewriter (AST → deparse)
                                     ├─▶ executor (hardened DuckDB)
                                     │       │
                                     │       └─▶ driver.Attach ──▶ upstream DB
                                     │
                                     └─▶ audit    (canonical JSON + hash chain)
```

Every terminal path emits exactly one audit record. The policy decision, the rewritten SQL, the parameter list, and the outcome (success/deny/reject/error/timeout) are all captured. Streaming paths emit on iterator close.

## 4. STRIDE per component

### 4.1 Parser (`internal/parser`, `internal/pgquery`)

- **Spoofing / Tampering:** N/A — parser is internal, takes bytes in and returns an AST.
- **Repudiation:** N/A — no side effects.
- **Information disclosure:** a malformed input must not leak internal state (panic, stack trace). Mitigated by `recover()` in handlers, `ParseError` with line/column only, and `FuzzParse` running nightly.
- **Denial of service:** algorithmic complexity and memory explosion from deeply nested queries. Mitigated by request body cap (`http.MaxBytesReader`, 1 MiB default) and a query-timeout context. `ErrInputTooLarge` sentinel is emitted before parsing is attempted.
- **Elevation of privilege:** N/A.

### 4.2 Policy engine (`internal/policy`)

- **Spoofing:** identity is established upstream (identity middleware) — policy trusts `UserCtx` from context.
- **Tampering:** the snapshot is held in `atomic.Pointer`; writers must go through `ApplySnapshot` which is only invoked by the reload path. No in-place mutation.
- **Repudiation:** every evaluation emits an audit record with the full `Decision` (applied policies, active filters, active masks, reject reasons).
- **Information disclosure:** `policy.Engine.Explain` proxies to the same evaluator as `Execute` — admin can inspect decisions without running the query, and vice-versa; no divergent code path.
- **Denial of service:** CEL / template rendering must be bounded. CEL is currently declared-only; the template engine compiles once at load and renders via positional parameters (no dynamic code generation per query).
- **Elevation of privilege:** default-deny when no `SqlAccessPolicy` allows. Verified by an invariant test on every policy-touching slice.

### 4.3 Rewriter (`internal/rewriter`)

- **Spoofing / repudiation:** N/A.
- **Tampering:** the rewriter never concatenates subject/request values into SQL. Templates are rendered to positional `$N` params and passed alongside the rewritten statement. A SQL-injection invariant test asserts no literal user data leaks into the deparsed output when a row filter uses `{{ subject.tenant_id }}`.
- **Information disclosure:** the deparsed SQL **is** logged (to audit), and by design — that is the record of what actually ran. The parameter values are also captured. Operators who must redact values can configure a downstream audit sink that masks before forwarding.
- **Denial of service:** expand-star is lazy (only fires when a FROM-table has an active filter or mask). Deparse is bounded by the AST size, which is bounded by the request body cap.
- **Elevation of privilege:** the statement-kind gate rejects writes (INSERT/UPDATE/DELETE/MERGE) with `ACL_REJECTED` and DDL/COPY/ATTACH/LOAD/INSTALL with `ERR_UNSUPPORTED_SYNTAX` — DuckDB never sees a mutation from a read-only SqlAccess policy.

### 4.4 Executor (`internal/executor`)

- **Tampering:** hardening disables external file access (`external_access=false`), blocks community extensions, disables autoinstall / autoload, disables persistent secrets, and locks configuration after init. `AttachHook` runs first (extensions need external access to install), then hardening, then `lock_configuration=true`.
- **Information disclosure:** DuckDB does not have a secret-leak surface once `allow_persistent_secrets=false` and external access are off. Driver-created secrets via `CREATE SECRET` are connection-scoped.
- **Denial of service:** per-request `context.WithTimeout` + pool sizing (`MaxOpen=4`, `MaxIdle=2`). A hostile query hitting the timeout surfaces as `CodeTimeout` and is audited.
- **Elevation of privilege:** the empty ATTACH URL + named-secret pattern used by the postgres/mysql drivers keeps credentials out of DuckDB's query log and crash dumps.

### 4.5 Identity (`internal/identity`)

- **Spoofing:** API-keys verified via HMAC-SHA256 against a server-wide pepper stored in `secret://`. JWTs verified against a JWKS per issuer; `alg: none` is explicitly rejected; algorithm allowlist is HS/RS/ES 256 / 384.
- **Tampering:** JWKS cache has a per-URL mutex that collapses concurrent unknown-kid fetches into one request, and a 30 s rate limit between repeat unknown-kid refreshes — a hostile client cannot force unbounded fetches by rotating the kid header. Stale-serve protects against transient IdP outages.
- **Repudiation:** every authenticated request carries a `UserCtx` that is recorded in the audit record (subject, issuer, email, groups, request-id, remote-addr).
- **Information disclosure:** API-key failures add bounded random jitter so unknown-key-id is indistinguishable from bad-HMAC by response time. `WWW-Authenticate: Bearer realm="sluice"` on 401 doesn't reveal which method would have worked.
- **Denial of service:** the identity middleware is O(1) per request; JWKS cache refresh is bounded (see tampering).
- **Elevation of privilege:** `AllowAnonymous` only applies to `ErrNoCredential`. A presented-but-invalid credential always 401s; there is no fall-through path where a bad credential is quietly downgraded to anonymous.

### 4.6 Audit (`internal/audit`)

- **Tampering:** the hash chain (`hash_i = sha256(hash_{i-1} || "\n" || canonical_i)`) makes any single-record or mid-chain modification detectable. `VerifyError.Is(ErrChainBroken)` is the standard way callers detect tampering.
- **Spoofing:** the genesis seed anchors the chain. A replay attacker with a different seed produces a chain whose first record's `prior_hash` mismatches what the verifier expects. Operators should store the seed in `secret://` and rotate only at a formal boundary.
- **Repudiation:** every terminal outcome emits exactly one record; the wrapped `RowIterator` emits on Close with the final row count.
- **Information disclosure:** records contain the rewritten SQL, the parameter values, the applied policies, and the caller's identity. The file sink enforces mode `0640` and the directory must be access-controlled by the operator.
- **Denial of service:** bounded dispatch queue (default 10 000) with non-blocking-then-deadlined send; when the queue is full `Enqueue` returns `ErrQueueFull` and increments `sluice_audit_dropped_total`. The executor still runs; the deny is the conservative choice for integrity-critical sinks, the drop-with-metric is safer for availability-critical deployments (configurable per deployment).

### 4.7 REST transport (`internal/transport/rest`)

- **Tampering / spoofing:** TLS termination is the operator's responsibility. Sluice validates identity per request; middleware order (body cap → timeout → panic recovery → identity) ensures the body cap takes effect before any parser work.
- **Information disclosure:** errors are rendered as `APIError` JSON with a canonical HTTP status; `X-Query-Id` is emitted on both success and error so operators can correlate a report with an audit record.
- **Denial of service:** body cap (1 MiB default), per-request timeout, panic recovery.

### 4.8 MCP transport (`internal/transport/mcp`)

Same threat profile as REST plus: MCP errors are returned as `CallToolResult{IsError: true}` so LLM clients can reason about them. This is a deliberate choice — a protocol-level error would break the agent; a tool-result error is self-describing and leaves the session alive.

### 4.9 Admin transport (`internal/transport/admin`)

- **Spoofing:** static-token auth with constant-time compare. Empty token logs a loud boot warning. Operators should bind the admin listener to a private interface.
- **Elevation of privilege:** read-only in MVP — no endpoint mutates state except `POST /admin/reload` which re-reads the on-disk config directory. The reloader trips the same code path as fsnotify / SIGHUP.
- **Information disclosure:** `/admin/subjects/explain` reveals the policy decision for an arbitrary subject; this is intentional for operator use and is gated by the admin token.

### 4.10 Configuration and secrets

- `secret://<provider>/<path>` resolution caches with per-entry TTL and makes defensive copies on get/set — a caller that mutates a returned slice cannot poison subsequent lookups.
- The `file://` provider rejects group/world-writable secret files (perm mask 0o022) and warns on world-readable (0o004).
- The audit genesis seed should be stored separately from the audit log itself so a disk-level attacker cannot rewrite both.

### 4.11 Datasource drivers (`internal/datasource/drivers/*`)

- Each driver validates identifiers against `[A-Za-z_][A-Za-z0-9_$]*` before interpolating catalog names into `CREATE SECRET` / `ATTACH` statements.
- `CREATE OR REPLACE SECRET` uses a named secret + empty ATTACH URL so passwords never appear in DuckDB's query log.
- `s3parquet`'s `PathAllowed` glob enforces the `allowedPaths` whitelist — `read_parquet('<path>')` views are only created for paths that match.
- `motherduck.SET motherduck_token` is connection-local; the token is released when the `*sql.Conn` returns to the pool.

## 5. Attack surface inventory

| Surface                               | Default exposure             | Notes                                             |
|---------------------------------------|------------------------------|---------------------------------------------------|
| REST `/v1/query`                      | Operator-controlled network  | Identity required; body cap; timeout.             |
| REST `/v1/health`, `/v1/ready`        | Operator-controlled network  | No identity; returns liveness only.               |
| REST `/v1/version`                    | Operator-controlled network  | No identity; returns build identity.              |
| REST `/openapi.json`                  | Operator-controlled network  | Static stub in MVP.                               |
| MCP stdio                             | Local process                | Parent process supplies the identity context.     |
| MCP Streamable HTTP                   | Operator-controlled network  | Identity bridged into request context.            |
| Admin `/admin/*`                      | Private network only         | Static token; constant-time compare.              |
| Prometheus `/metrics`                 | Private network only         | Plaintext metric exposure; no identity by design. |
| Config + policy directory             | Filesystem                   | Operator-owned; watched via fsnotify.             |
| Audit directory                       | Filesystem                   | Operator-owned; should be append-only.            |

## 6. Mitigations summary

- **Default-deny.** Empty or missing policy directory rejects every query. Invariant tested on every relevant slice.
- **Parameterised rewriting.** Templates render to positional `$N`; no user data flows into SQL literals.
- **Hardened executor.** External file access off, community extensions off, configuration locked after init.
- **Hash-chained audit.** Tamper-evident by construction; genesis-seed anchor prevents replay from a different installation.
- **Signed-and-pinned dependencies.** `go.sum` checked in; `go mod tidy` is CI-enforced; `govulncheck` runs on every PR.
- **SPDX headers + license separation.** Every source file carries a header; `scripts/check-spdx.sh` enforces in CI; `pkg/` cannot import `internal/` (depguard).
- **Constant-time comparisons** for admin token and API-key HMAC.
- **Bounded fetches** for JWKS (per-URL mutex + rate limit) and for audit dispatch (bounded channel).
- **Release artifact integrity.** cosign-signed binaries + multi-arch Docker images with syft SBOM and SLSA provenance (landing with the release matrix).

## 7. Known limitations

- **CASE-expression oracle in column masks.** A policy that masks `email` to `'***'` but allows `WHERE email = 'victim@example.com'` can leak existence through result-set shape. Operators who need constant-time behaviour must either deny such predicates via `QueryRejectPolicy` or accept the oracle. Documented at `docs/security/column-mask-oracle.md` (landing with Slice 9).
- **CEL is declared-only.** `SqlAccessPolicy.condition` and `QueryRejectPolicy.rules[].when` parse today but do not evaluate. A policy that relied on a CEL condition to deny would currently fall through; the engine rejects unknown CEL at load time to make this visible. CEL GA lands in v0.2.
- **No write path.** MVP is read-only. `WritableDataSource` is declared but not implemented; operators who need write-through must run a separate gateway.
- **Single-instance audit chain.** Hash-chaining within one instance is straightforward; horizontal scaling requires an external ordering guarantee (Kafka, Kinesis, etc.) and is deferred to v1.
- **No OIDC discovery.** JWKS URLs must be configured explicitly per issuer in MVP; OIDC `/.well-known/openid-configuration` auto-discovery lands in v0.3.
- **MotherDuck live integration is best-effort.** The driver is wired and unit-tested, but live round-trips depend on a nightly lane gated on a token.

## 8. Residual risk

Even with every mitigation above, a sufficiently privileged insider (direct filesystem access, ability to rotate the genesis seed, ability to push a signed release) can undermine the guarantees. Sluice is a control plane; it cannot substitute for the operator's host-, network-, and supply-chain hygiene.

The explicit residual risks we acknowledge:

1. An insider who can both rotate the audit genesis seed and replace the audit log can produce a consistent but forged chain. Mitigation: store the seed in a separate KMS; back the audit directory with a WORM volume.
2. A vulnerability in DuckDB, pg_query, or a driver's upstream extension — Sluice pins versions, monitors `govulncheck`, and patches promptly, but the fix window is owned by upstream.
3. Policy misconfiguration. Sluice validates structure, but semantic correctness (does this policy actually match your compliance requirements?) is the operator's responsibility. `policy explain` and the golden fixture harness are aimed at this.

## 9. Security contact

- `security@bino.bi` for reports.
- `conduct@bino.bi` for code-of-conduct matters.
- GitHub private vulnerability reporting on `bino-bi/sluice`.

See `SECURITY.md` for the full disclosure process and SLA.

---

Last reviewed: 2026-04-20. This document is amended by PR; material changes go through the RFC process in [GOVERNANCE.md](GOVERNANCE.md).
