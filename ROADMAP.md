<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# Roadmap

Sluice is released under AGPL-3.0 and under active development. This roadmap describes where we are and where we're going. Dates are indicative, not commitments. See `CHANGELOG.md` for what has actually shipped.

## Now: v0.1.0

The first public release. Sluice enforces access policies, row filters, column masks, query rejections, approvals and audit on SELECTs routed through DuckDB.

- Data sources: SQLite file, DuckDB file, Postgres, MySQL, files in S3 and behind HTTP URLs (Parquet, CSV, JSON, Excel), MotherDuck.
- Policy kinds: `SqlAccessPolicy`, `RowFilterPolicy`, `ColumnMaskPolicy`, `QueryRejectPolicy`, `QueryRewritePolicy` (limit, sample, timeout), `ApprovalPolicy`, `SubjectBinding`, `DataClassification`, `RelationshipPolicy`.
- CEL conditions on policies and CEL expressions in reject rules.
- Mask types: `null`, `constant`, `partial`, `hash`, `regex`, `truncate`, `jitter`, `fpe`, `fake`.
- Policy engines: YAML, OPA (Rego modules), ReBAC through OpenFGA, and a composite of them.
- Transports: REST (`POST /v1/query`, JSON and CSV streaming), MCP (stdio and Streamable HTTP, paginated list tools), admin API. TLS on every HTTP listener, mutual TLS on REST and MCP.
- Identity: API keys (HMAC-SHA256 with pepper), JWT with JWKS cache, several issuers.
- Guardrails: rate limits, concurrency limit, per-subject daily budgets, row and result size limits.
- Audit: hash-chained JSONL file sink with daily and size rotation, optional keyed chain, syslog and S3 sinks (S3 with Object Lock), `sluice audit verify`.
- Observability: Prometheus metrics, OpenTelemetry tracing.
- CLI: `serve`, `mcp`, `version`, `config validate`, `policy validate/explain/test`, `datasource check`, `audit verify`, `schema export`, `apikey hash`, `budget show`.
- Hot reload: fsnotify, SIGHUP and `POST /admin/reload`.
- Release artifacts: signed multi-arch binaries and Docker images, SBOM, SLSA provenance.

Status: `v0.1.0-rc.3` is out. The final release follows when the candidate shows no blockers.

## Next: v0.2.0

- Audit sink fan-out: Postgres and OTLP logs.
- Driver-level OpenTelemetry spans (otelsql) for DuckDB calls.
- `custom` and `external` mask types. Today the schema accepts them, but a policy that uses one does not load.

## Soon: v0.3.0 / v0.4.0

- Additional identity providers: OIDC discovery, identity from the client certificate for mTLS clients (transport-level mTLS gating already shipped).
- Secret providers behind build tags: Vault, AWS Secrets Manager, GCP Secret Manager. Today only `secret://env/...` and `secret://file/...` work.
- Admin API: `POST /admin/audit/export`, `POST /admin/datasources/:name/probe`.
- Benchmarks as CI regression gate (today they're informational).

## v1.0

The v1.0 bar is **production-ready for regulated multi-tenant deployments**. Summary:

- Identity: OIDC + mTLS GA. Refresh-token rotation.
- Policies: CEL GA, `QueryRewritePolicy` GA, per-binding `spec.claims` with wildcards.
- Audit: append-only filesystem support (Linux `chattr +a`), WORM volumes validated, real-time S3 Object Lock path.
- Drivers: write-path (`WritableDataSource`) for a subset. `ReloadableDataSource` for live spec rotation.
- Transports: gRPC + Connect. Admin `/admin/audit/query`.
- Deployment: Kubernetes operator, Helm chart, horizontal scaling with shared schema cache.
- Docs: compliance mappings (GDPR Art. 30, BSI C5, SOC 2) with external audit.

## Beyond v1: v2

Directional, not committed.

- Pure-Go parser backend (cockroachdb-parser) so `CGO_ENABLED=0` is default.
- OPA bundles as a policy source, next to the Rego module directory supported today.
- Streaming Arrow over HTTP2 + Flight.
- Federation across Sluice instances.
- Commercial-license-gated enterprise add-ons (SSO directory sync, field-level encryption at rest, policy-as-code CI/CD platform).

## How to influence the roadmap

- **Bugs / small features:** open a GitHub issue.
- **Large proposals:** open a Discussion first. If the direction is accepted, follow up with an RFC under `docs/rfcs/` per [GOVERNANCE.md](GOVERNANCE.md).
- **Sponsorship:** the roadmap is prioritised by maintainer time. Commercial support and sponsorship arrangements can move specific items up. Contact `hello@bino.bi`.

---

Last updated: 2026-10-14.
