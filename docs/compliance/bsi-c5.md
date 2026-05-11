<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# BSI C5

The BSI Cloud Computing Compliance Criteria Catalogue (C5:2020) covers
17 objective areas. Sluice's relevant mechanisms cluster around
**IDM** (identity), **KRY** (cryptography), **BCM** (backup), **SIM**
(incident management), and **OPS** (operations).

| Area      | Control (sample)                             | Sluice mechanism                                                    |
| --------- | -------------------------------------------- | ------------------------------------------------------------------- |
| IDM-01    | Identity lifecycle                           | SubjectBinding objects tie issuers to canonical subjects.           |
| IDM-09    | Privileged access monitoring                 | Admin port auth logs every call with `request_id` + `subject`.      |
| KRY-01    | Cryptographic usage guidelines               | Algorithm allow-list on JWT; HMAC-SHA256 for API keys.              |
| KRY-04    | Key rotation                                 | Pepper + JWKS + genesis rotation documented under [security/key-rotation](../security/key-rotation.md). |
| BCM-03    | Audit-log continuity                         | Hash chain; `sluice audit verify` detects tamper or gap.            |
| SIM-03    | Evidence preservation                        | Append-only JSONL; file permissions 0640; rotation preserves prior files. |
| OPS-04    | Configuration management                     | Policies live in a git-tracked directory; reloads atomic via `config.Registry`. |
| OPS-09    | Vulnerability management                     | `govulncheck`, `gosec`, `trivy` lanes in CI; CodeQL weekly.         |

For a full attestation, engage a BSI-qualified auditor. The binary
image is reproducible via goreleaser-cross; SBOMs (CycloneDX) are
attached to every release and tell the auditor exactly which
dependencies shipped in a given version.
