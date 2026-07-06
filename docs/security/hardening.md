<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Hardening checklist

Walk this list before serving production data. Most items map directly to a
config field in [`sluice.yaml`](../reference/configuration.md) or a policy in
your `policies.d/`.

## Process

- [ ] Run as a non-root user. The repository's `Dockerfile` builds a
      distroless image pinned to `nonroot:nonroot` (uid 65532); replicate
      that in your own builds.
- [ ] Mount the policy and datasource directories read-only (`:ro` in
      Docker, `readOnly: true` in Kubernetes).
- [ ] Keep secret files strict: Sluice **refuses** to read a `secret://file`
      target that is group- or world-writable and warns when it is
      world-readable. `chmod 0600` is the safe default.
- [ ] Give the audit directory its own persistent volume (created `0750`,
      files `0640`).

## Network

- [ ] Enable TLS on the REST listener (`rest.tls.certFile` /
      `rest.tls.keyFile`) or terminate TLS at a proxy in front of it. Sluice
      logs a warning when serving plain HTTP.
- [ ] Bind the admin listener (`admin.listen`, default `:9091`) to a private
      interface. It carries `/admin/*` and `/metrics` — never expose it to
      the internet.
- [ ] Set a strong admin token. An empty token boots in dev mode with only a
      logged warning between you and an open admin plane.

!!! warning "Not yet implemented: mTLS"
    The config schema parses `rest.tls.clientCA` and `rest.tls.clientAuth`,
    but client-certificate verification is not enforced — the REST server
    loads a single server cert/key pair. If you need mutual TLS, enforce it
    at a proxy in front of Sluice.

## Credentials

- [ ] Set `identity.apiKeyPepper` to a `secret://` reference. Without it,
      API-key authentication is disabled entirely.
- [ ] Pass the admin token via the environment
      (`SLUICE_ADMIN__TOKEN=...`) rather than writing it into `sluice.yaml`.
- [ ] For JWT bindings, pin `issuer`, `audience`, **and** `jwksUrl` in each
      `SubjectBinding` — never trust `iss` alone.
- [ ] Use read-only database credentials for every `DataSource`
      (`credentialsRef` via `secret://`). Sluice attaches sources
      `READ_ONLY`, but a least-privilege upstream grant is defense in depth.
- [ ] For `s3_parquet`, pin `allowedPaths` to the minimum set of globs.
- [ ] Plan rotation up front — see [Key rotation](key-rotation.md).

## Audit

- [ ] Keep `audit.failClosed: true` (the default). Queries that cannot be
      audited return `ERR_AUDIT_UNAVAILABLE` instead of leaking unrecorded.
- [ ] Pin `audit.file.genesis` to a stable secret so the hash chain stays
      verifiable across redeploys.
- [ ] Run `sluice audit verify --anchor ...` on a schedule and alert on
      `sluice_audit_dropped_total` and `sluice_audit_write_errors_total`.
      Details in [Audit trail](audit.md).

## Policies

- [ ] Put a rate limit **and** a budget on every agent-facing
      `SubjectBinding`:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: SubjectBinding
metadata:
  name: report-bot
spec:
  apiKeys:
    - id: report-bot
      hashRef: secret://env/REPORT_BOT_KEY_HASH
      groups: ["agents"]
  rateLimit:
    rps: 5
    burst: 10
  budget:
    cpuSecondsPerDay: 600
    rowsPerDay: 1000000
```

- [ ] Add a global `QueryRewritePolicy` backstop so no statement runs
      unbounded, even where nothing more specific matches:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: QueryRewritePolicy
metadata:
  name: global-backstop
  priority: 10
spec:
  match:
    any:
      - resources:
          tables: ["**"]
  rewrite:
    limit:
      max: 10000
    timeout: 15s
```

- [ ] Verify default-deny in CI with a [policy test](../policies/testing.md)
      that asserts an unmatched subject is denied:

```yaml
# policies.d/tests/default-deny.yaml
cases:
  - name: unknown subject is denied
    identity:
      subject: nobody
    sql: SELECT id FROM shop.main.customers
    expect:
      outcome: deny
      errorCode: ACL_DENIED
```

- [ ] Roll new enforcement policies out with `enforcementMode: DryRun`
      first, watch the audit log for would-be denials, then promote to
      `Enforce`. See [Configuration reload](../operations/hot-reload.md) for
      the zero-downtime path.
