<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Subjects and authentication

`SubjectBinding` declares how callers authenticate and what per-subject limits apply. Every
request resolves to a subject carrying an id, issuer, email, groups, and claims — the inputs for
[selectors](matching.md), `{{ subject.* }}` templates, and [CEL conditions](conditions.md).
Without a matching binding, JWTs fail on their unknown issuer and API keys cannot verify: the
request gets a 401 `ERR_UNAUTHORIZED`.

## JWT bearer tokens

One binding per issuer (a duplicate issuer is a load error). Accepted algorithms are HS256/384,
RS256/384, and ES256/384 — `alg: none` is always rejected. RS/ES keys come from the JWKS
endpoint; HS* uses the shared secret in `hmacSecretRef`. Sluice validates `iss`, `aud`, `exp`,
`nbf`, and `iat`.

| Field | Meaning |
| ----- | ------- |
| `issuer` | The `iss` claim this binding serves |
| `audience` | Required `aud` value (at least one entry must match) |
| `jwksUrl` | JWKS endpoint for RS/ES keys — requires both `issuer` and `audience` to be set |
| `hmacSecretRef` | `secret://` ref of the shared secret for HS256/384 |
| `jwksCacheTtl` | How long fetched JWKS keys are cached |
| `clockSkew` | Tolerance on `exp`/`nbf`/`iat` (default 60s) |
| `claims` | Claim paths mapped onto subject attributes (below) |

### Claim paths

`spec.claims` maps JWT claims onto the well-known attributes `subjectId`, `email`, `groups`,
`tenantId`, and `allowedRegions`; any extra key in the block becomes an additional named claim.
Paths use a JSONPath-lite grammar: the leading `$` is optional, segments are dot-separated
(`$.realm_access.roles`), and brackets index arrays (`$.tenants[0]`). Wildcards, filters, and
recursive descent are not supported. `groups` accepts a JSON list or a comma-separated string.

Extracted values flow into selector matching (`subjects.jwtClaims`, `subjects.groups`),
[row-filter templates](row-filters.md) like `{{ subject.tenantId }}`, and CEL via
`subject.claims`.

## API keys

Callers present `X-Api-Key: <id>.<material>` or `Authorization: ApiKey <id>.<material>`.
Verification is a constant-time comparison of HMAC-SHA256(pepper, `id:material`) against the hex
hash behind `apiKeys[].hashRef` — the server never stores key material, and the server-wide
pepper (`identity.apiKeyPepper`, a `secret://` ref) means a leaked hash cannot be replayed as a
key. Failures get a bounded random timing jitter so unknown-id and bad-hash are
indistinguishable.

Generate the hash once, at key issuance:

```console
$ sluice apikey hash --pepper secret://env/SLUICE_APIKEY_PEPPER \
    --id reporting-bot --material 'k3Q…long-random-secret…'
9f2c4a…64-hex-chars…
```

Store that hex string wherever the `hashRef` points (an env var or file). In API-key mode the
`claims` block holds **literal values**, not paths: `subjectId` becomes the subject id (falling
back to the key id), and `tenantId`, `email`, and extra keys are stamped onto the session so
templates like `{{ subject.tenantId }}` work without a JWT. Groups come from each key's
`groups` list. Each `apiKeys[]` entry has exactly three fields — `id`, `hashRef`, `groups`.

## Rate limits

`spec.rateLimit: {rps, burst}` gives each subject a token bucket. The spec is resolved by
subject id first, then by issuer — so an API-key binding limits its one subject, while a JWT
binding's limit applies per authenticated subject under that issuer. `rps: 0` (or no `rateLimit`
block) means unlimited; anonymous requests are bounded by the global `limits.maxConcurrent`
gate instead. Over-limit requests get `ERR_RATE_LIMITED` (HTTP 429). Limits are
[hot-reloadable](../operations/hot-reload.md); a changed spec resets the bucket.

## Daily budgets

`spec.budget: {cpuSecondsPerDay, rowsPerDay}` caps what a subject may consume per UTC day; a
zero field leaves that dimension unlimited. Usage (CPU milliseconds and rows served) is recorded
as each query completes, held in memory, and flushed to an embedded SQLite store so restarts
resume near where they left off. Counters reset at UTC midnight. An exhausted budget returns
`ERR_BUDGET_EXCEEDED` (HTTP 429). Accounting is post-execution — the budget is checked before a
query runs and charged after it completes — so a single very large query can overshoot the daily
cap before it is counted; treat budgets as a daily backstop, not a per-query limit.

Budgets are opt-in on the server side:

```yaml
# fragment of sluice.yaml — server configuration, not a policy
identity:
  apiKeyPepper: secret://env/SLUICE_APIKEY_PEPPER
budget:
  enabled: true
  stateDir: ./state      # SQLite database location
  flushInterval: 5s
  failClosed: true       # budget store unavailable => refuse queries
  retentionDays: 35
```

Inspect a subject's consumption with
`sluice budget show <subject> [--state-dir ./state] [--day YYYY-MM-DD] [--json]`.

## Recipes

**BI service account via the corporate IdP** — JWKS-verified JWTs, claims feeding selectors and
tenant templates, a generous shared rate limit:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: SubjectBinding
metadata: { name: corp-idp }
spec:
  issuer: https://idp.example.com/realms/corp
  audience: sluice
  jwksUrl: https://idp.example.com/realms/corp/protocol/openid-connect/certs
  jwksCacheTtl: 10m
  clockSkew: 60s
  claims:
    subjectId: $.sub
    email: $.email
    groups: $.realm_access.roles
    tenantId: $.tenant
  rateLimit: { rps: 20, burst: 40 }
```

**Agent API key with a tight leash** — fixed identity, low rate, hard daily budget:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: SubjectBinding
metadata: { name: research-agent }
spec:
  claims:
    subjectId: research-agent   # literal in API-key mode
    tenantId: acme
  apiKeys:
    - id: research-agent
      hashRef: secret://env/RESEARCH_AGENT_KEY_HASH
      groups: ["agents"]
  rateLimit: { rps: 2, burst: 5 }
  budget:
    cpuSecondsPerDay: 600
    rowsPerDay: 500000
```
