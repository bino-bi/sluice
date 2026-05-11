<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# Security Policy

Sluice enforces access control on SQL traffic between clients and data sources. Security vulnerabilities in this project can materially expose customer data, so we take reports seriously and respond quickly.

## Supported versions

Sluice is pre-1.0. Until `v1.0.0` ships, only the most recent minor release receives fixes. At v1.0.0 this table becomes:

| Version | Supported                    |
|---------|------------------------------|
| 1.x.x   | Yes — current stable         |
| 0.x.x   | Security fixes for 6 months  |
| older   | No                           |

## Reporting a vulnerability

Please **do not** file security issues in public GitHub issues, Discussions, or pull requests.

**Preferred:** GitHub private vulnerability reporting on this repository — *Security* tab → *Report a vulnerability*.

**Alternative:** email `security@bino.bi`. A PGP key will be published at `https://bino.bi/.well-known/pgp-key.asc` before v0.1.0; encrypt any report containing credentials, query samples, or exploit code.

Please include, where possible:

- Sluice version (`sluice version --json` output).
- Affected component (REST, MCP, admin, parser, policy engine, executor, audit, identity, datasource driver).
- Reproduction steps, proof-of-concept, and minimal configuration.
- Your assessment of impact (data exfiltration? policy bypass? audit tampering? DoS?).
- Whether you've disclosed the issue to anyone else.

## Response SLA

| Stage                  | Target                                           |
|------------------------|--------------------------------------------------|
| Acknowledge receipt    | 72 hours                                         |
| Initial triage         | 5 business days                                  |
| Fix or mitigation plan | 14 days for high/critical, 30 days for moderate  |
| Coordinated disclosure | Within 90 days unless mutually extended          |

We will keep you informed as the fix progresses and credit you in the advisory unless you ask otherwise.

## Severity guidance

We map reports to CVSS v3.1 vectors. The rough buckets we work against:

- **Critical** — unauthenticated policy bypass, silent audit tampering, code execution on the Sluice host, exfiltration of multiple tenants' data.
- **High** — authenticated policy bypass that exposes another subject's rows or unmasked columns; escape of the read-only executor; signed-audit chain forgery with partial knowledge.
- **Moderate** — information leak that requires unusual preconditions; DoS against a single Sluice instance (not the upstream database); admin API auth weaknesses when the admin port is exposed.
- **Low** — issues behind multiple defence-in-depth layers; best-practice deviations without a concrete impact path.

## Out of scope

The following do not qualify for an advisory, though we still welcome reports:

- Social engineering of maintainers or contributors.
- Denial of service achieved by sending legitimate-but-expensive queries to a test instance (Sluice is designed to timeout and shed load — reports that a specific SQL shape consumes resources are a performance bug, not a security issue).
- Vulnerabilities in transitive dependencies that have no exploitable path through Sluice — please report upstream.
- Attacks that require already having administrative access to the host, the config directory, or the audit directory.
- Missing security headers on the bundled demo docker-compose (it's a demo; please harden your deployment).

## Safe harbour

We will not pursue legal action against researchers who:

- Make a good-faith effort to avoid privacy violations, service degradation, or data destruction.
- Only interact with accounts or instances you own or have explicit permission to test.
- Give us a reasonable window (see SLA above) to remediate before public disclosure.
- Do not exploit the issue beyond what is necessary to confirm it.

## Hall of fame

Confirmed reporters will be listed in `CHANGELOG.md` under the fixing release and in a dedicated section of the public security page once one is published.

## Operator hardening checklist

If you run Sluice in production, the following reduce the blast radius of a vulnerability:

- Keep the admin port bound to `127.0.0.1` or a private network; gate it with network ACLs in addition to the static token.
- Store the API-key pepper and the audit genesis seed in a real secret manager (`secret://vault/...` once the v1 providers land), never in a file on the same disk as the audit log.
- Run the container as the non-root user baked into the image (default in the published `Dockerfile`).
- Mount the audit directory on an append-only or WORM-backed volume and ship it offsite in real time.
- Pin the container image by digest, not tag; verify the cosign signature before rollout.

---

Questions about this document can be sent to `security@bino.bi`.
