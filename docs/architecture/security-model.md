<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Security model

Sluice is built fail-closed: when a component cannot prove a request is safe, the request is
refused. This page inventories those guarantees and the trust boundaries they defend.

## Fail-closed by design

- **Default-deny.** An empty `policies.d/` is a valid configuration that denies every query. No
  matching `effect: allow` policy — or no active snapshot at all — means `ACL_DENIED`.
- **Read-only statement allowlist.** The rewriter and executor accept only `SELECT`, `EXPLAIN`,
  `SET`, `SHOW`, and `PRAGMA`. `INSERT`/`UPDATE`/`DELETE` return `ACL_REJECTED` ("write operations
  are not permitted"); DDL, `COPY`, and `ATTACH` return `ERR_UNSUPPORTED_SYNTAX`.
- **Fail-closed audit.** By default (`audit.failClosed: true`) no row is served until the access
  audit record is durably enqueued; otherwise the query is refused with `ERR_AUDIT_UNAVAILABLE`.
  Setting `audit.failClosed: false` switches to best effort: an enqueue failure is logged and the
  query proceeds — accept that trade only if availability outranks your audit obligation.
- **Condition and template errors deny.** A CEL condition that errors at runtime, or a policy
  template referencing a variable the subject does not carry, denies the whole request rather than
  guessing.
- **OPA strict output.** A Rego decision with unknown fields — or no decision at all — fails
  closed; it is never interpreted as an allow.
- **ReBAC backend errors deny.** An OpenFGA check that errors fails the request; errors are never
  cached as answers.
- **Approval broker limits.** A full approval queue refuses new approval-gated queries
  (`ERR_RATE_LIMITED`), and a decision requiring approval with no broker configured is refused
  outright.
- **Budget store failures.** With `budget.failClosed: true` (the default), a budget store that
  cannot be opened at startup aborts the server instead of silently running without budgets;
  with `false` the error is logged and budgeting is disabled.

## Trust boundaries

### Client ↔ Sluice

Callers authenticate with a JWT bearer token or an API key, resolved to a `SubjectBinding`. JWTs
accept HS256/384, RS256/384, and ES256/384; `alg=none` is rejected; `iss`, `aud`, `exp`, `nbf`, and
`iat` are validated with bounded clock skew. API keys are verified with a constant-time HMAC-SHA256
comparison against the binding's `hashRef` secret, with timing jitter added on failure so neither
key id nor material leaks through response timing.

### Sluice ↔ data sources

Credentials are referenced as `secret://` URIs (`credentialsRef`/`tokenRef`, `env` and `file`
schemes) and injected via DuckDB `CREATE SECRET`, so secret values never appear in `ATTACH`
statements, logs, or audit records; the credential-free connection string itself lives in the
datasource manifest. Every catalog is attached to DuckDB
`READ_ONLY`, so even a bug that slipped a write past the statement allowlist would hit a read-only
attachment.

!!! warning "Not yet implemented"
    `secret://vault/…`, `secret://aws-sm/…`, and `secret://gcp-sm/…` references parse but do not
    resolve yet. Use `env` or `file`.

### Admin plane

The admin API listens on its own address (default `:9091`, off by default) and authenticates with a
static bearer token compared in constant time. An empty token is a loud dev-mode warning, not a
production configuration. Prometheus metrics are served on this listener behind the same token.

## Approval capability URLs

Approval decisions travel as capability URLs: the webhook payload carries `accept_url` and
`reject_url` containing a 32-byte random token (base64url). Tokens are compared in constant time;
an unknown approval id and a bad token produce byte-identical 404 responses, so there is no oracle
to probe. Link prefetchers and unfurlers (HEAD requests, `Purpose: prefetch` headers) never mutate
state, and an approval mints a single-use grant that only the same subject re-running the identical
SQL can consume.

## Secret hygiene

Secret byte values never appear in logs, panics, metrics, or audit records. Log statements wrap
sensitive values in the `telemetry.Redacted` helper, which renders as `[redacted]`; audit records
carry SQL fingerprints and a bounded SQL sample, never resolved credentials.

## Non-goals

Sluice does not defend against a compromised host (an attacker with the process's memory has its
secrets), a malicious administrator (the admin token holder can change policy), or aggregate
inference — a caller allowed to run aggregates may still learn about individuals through repeated
statistical queries. The full analysis, including STRIDE tables, is in the
[threat model](../security/threat-model.md).
