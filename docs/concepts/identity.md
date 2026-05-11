<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Identity

Sluice resolves every request to a `UserCtx` carrying the subject,
issuer, email, groups, claims, auth method, request ID, and remote
address. Downstream policy evaluation, templating, and audit emission
all read from this single struct.

## Authentication methods (MVP)

- **JWT Bearer** — `Authorization: Bearer <token>`. Algorithm allow-list
  is `HS/RS/ES 256/384`. `alg: none` is always rejected. Per-issuer
  `SubjectBinding` objects bind a JWKS URL, audience, and claim paths.
- **API key** — `Authorization: ApiKey <id>.<material>` or
  `X-Api-Key: <id>.<material>`. Verification is HMAC-SHA256 over the
  material with a server-wide pepper. A bounded random jitter prevents
  timing side-channels between unknown-id and bad-HMAC outcomes.

A `Composite` identifier walks its children in registration order and
returns the first success. If every child returns `ErrNoCredential`,
the composite returns `ErrNoCredential`; the middleware then either
emits `401` (default) or forwards an anonymous UserCtx
(`AllowAnonymous=true`, loudly warned at boot).

## Claim extraction

`SubjectBinding.spec.claims` uses a JSONPath-lite grammar:

- `$.sub` — top-level claim.
- `$.realm_access.roles` — nested path.
- `$.custom.tenants[0]` — bracket indexing.

Wildcards, filter expressions, and recursive descent are intentionally
not supported in MVP. Anything more complex belongs in an upstream
identity proxy.

## Context propagation

Identity lives on `context.Context` via a private key. Transport
middleware installs it on inbound requests; the audit layer reads it
back out at emission time. Nothing outside `internal/identity` can
fabricate a UserCtx — only the middleware or the composite can.
