<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Hardening checklist

Before running Sluice in production, walk through this list. Most
items map directly to a config knob or a filesystem permission.

## Process

- [ ] Run as a non-root user. The distroless image already pins
      `nonroot:nonroot` (uid 65532).
- [ ] Mount the policies directory read-only (`:ro` in Docker,
      `readOnly: true` in Kubernetes).
- [ ] Set `umask 0027` so newly-written audit files are not
      world-readable.
- [ ] Enable `no-new-privileges` and a seccomp profile (docker-default
      suffices).

## Network

- [ ] Put the REST listener behind a TLS-terminating proxy or enable
      `rest.tls.*` in server.yaml.
- [ ] Bind the admin listener to an internal interface only.
- [ ] Never expose the admin listener to the public internet — the
      bootstrap logs a loud warning if the token is empty, but nothing
      stops you from exposing it anyway.

## Credentials

- [ ] Provide a real `identity.apiKeyPepperRef`. Booting without one
      disables API-key auth and logs a warning.
- [ ] Rotate the pepper and API keys on a cadence that matches your
      compliance posture. See [Key rotation](key-rotation.md).
- [ ] For JWT, pin issuer → JWKS URL via `SubjectBinding`. Do not trust
      `iss` alone.

## Audit

- [ ] Mount a persistent volume for `audit.file.dir`.
- [ ] Provide a stable `audit.file.genesisRef`. A new seed on every
      boot forfeits chain continuity across restarts.
- [ ] Run `sluice audit verify <dir>` on every rotation.
- [ ] Alert on `sluice_audit_dropped_total` > 0 and
      `sluice_audit_write_errors_total` > 0.

## Data sources

- [ ] Every `DataSource.spec.readonly` should remain `true` in MVP.
- [ ] For `s3_parquet`, pin the `allowedPaths` glob list to the
      minimum necessary.
- [ ] Prefer IAM roles over static S3 credentials when running on AWS.
