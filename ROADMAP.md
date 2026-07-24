<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# Roadmap

Sluice is released under AGPL-3.0 and under active development. This roadmap describes where we are and where we're going; dates are indicative, not commitments. See `CHANGELOG.md` for what has actually shipped.

## Now — v0.1.0 (MVP)

The first public release. Enforce row filters, column masks, query rejections, and audit on SELECTs routed through DuckDB to six data sources.

- Data sources: SQLite file, DuckDB file, Postgres, MySQL, S3 Parquet, MotherDuck.
- Policies: `SqlAccessPolicy`, `RowFilterPolicy`, `ColumnMaskPolicy`, `QueryRejectPolicy` (static expressions), `QueryRewritePolicy` declared-only.
- Mask providers: `null`, `constant`. (`partial` + `hash` in v0.2.)
- Transports: REST (`POST /v1/query`, JSON + CSV streaming), MCP (stdio + Streamable HTTP), read-only admin.
- Identity: API-Key (HMAC-SHA256 + pepper), JWT with JWKS cache. Multi-issuer.
- Audit: hash-chained JSONL file sink with daily + size rotation, `sluice audit verify`.
- CLI: `serve`, `version`, `config validate`, `policy validate/explain`, `datasource check`, `audit verify`, `schema export`.
- Hot reload: fsnotify + SIGHUP + `POST /admin/reload`.
- Release artifacts: signed multi-arch binaries + Docker images, SBOM, SLSA provenance.

Status: the query path and transports are in place; release matrix, docs site, and examples are the final gap.

## Next — v0.2.0

- `partial` and `hash` column-mask providers with Arrow-aware hashing.
- CEL condition evaluation for `SqlAccessPolicy.condition` and `QueryRejectPolicy.rules[].when`.
- `QueryRewritePolicy` runtime — limit injection, sampling, per-policy timeout.
- SELECT-* expansion for unmasked tables (today DuckDB handles it; explicit expansion gives operators deterministic output shape).
- Expanded golden rewrite fixtures covering every scenario in concept §4.11.
- ~~OTel tracing in `internal/telemetry` + HTTP middleware~~ (shipped; `WrapDB`/otelsql driver-level spans still pending).
- Audit sink fan-out: S3 with Object Lock, Postgres, syslog, OTLP logs.

## Soon — v0.3.0 / v0.4.0

- Additional identity providers: OIDC discovery, certificate-derived identity for mTLS clients (transport-level mTLS gating already shipped).
- Secret providers behind build tags: Vault, AWS Secrets Manager, GCP Secret Manager.
- `sluice policy test` — run a fixture table against a policy directory.
- Admin API: `POST /admin/audit/export`, `POST /admin/datasources/:name/probe`.
- Benchmarks as CI regression gate (today they're informational).

## v1.0

The v1.0 bar is **production-ready for regulated multi-tenant deployments**. Summary:

- Identity: OIDC + mTLS GA; refresh-token rotation.
- Policies: CEL GA, `QueryRewritePolicy` GA, per-binding `spec.claims` with wildcards.
- Audit: append-only filesystem support (Linux `chattr +a`), WORM volumes validated, real-time S3 Object Lock path.
- Drivers: write-path (`WritableDataSource`) for a subset; `ReloadableDataSource` for live spec rotation.
- Transports: gRPC + Connect; paginated MCP; admin `/admin/audit/query`.
- Deployment: Kubernetes operator, Helm chart, horizontal scaling with shared schema cache.
- Docs: compliance mappings (GDPR Art. 30, BSI C5, SOC 2) with external audit.

## Beyond v1 — v2

Directional, not committed.

- Pure-Go parser backend (cockroachdb-parser) so `CGO_ENABLED=0` is default.
- Embedded OPA bundle as an alternative policy engine.
- Streaming Arrow over HTTP2 + Flight.
- Federation across Sluice instances.
- Commercial-license-gated enterprise add-ons (SSO directory sync, field-level encryption at rest, policy-as-code CI/CD platform).

## How to influence the roadmap

- **Bugs / small features:** open a GitHub issue.
- **Large proposals:** open a Discussion first; if the direction is accepted, follow up with an RFC under `docs/rfcs/` per [GOVERNANCE.md](GOVERNANCE.md).
- **Sponsorship:** the roadmap is prioritised by maintainer time. Commercial support and sponsorship arrangements can move specific items up; contact `hello@bino.bi`.

---

Last updated: 2026-04-20.
