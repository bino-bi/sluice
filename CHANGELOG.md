<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# Changelog

All notable changes to Sluice are documented here. The format follows [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/) and this project adheres to [Semantic Versioning](https://semver.org/).

Entries for each release are grouped into **Added**, **Changed**, **Deprecated**, **Removed**, **Fixed**, and **Security**. Unreleased work is appended to the `[Unreleased]` section and rolls into the next tagged release.

## [Unreleased]

### Added

- **QueryRewritePolicy runtime** — `limit` (AST-level LIMIT injection/clamp), `sample` (DuckDB `USING SAMPLE` wrap), and `timeout` (executor override) are now enforced.
- **Column-mask providers** — SQL-expression masks `partial`, `hash` (sha256), `regex`, `truncate`; post-query masks `fpe` (FF1), `jitter`, `fake`, and `hash` (hmac_sha256). New error code `ERR_MASK_UNSUPPORTED_CONTEXT` when a post-query-masked column is used in a predicate/expression.
- **CEL** — policy `conditions`, CEL row-filter `expression`s (lowered to parameterised SQL), and CEL reject-rule `expression`s are evaluated (google/cel-go).
- **Rewrite/decision cache** (`internal/policycache`) — opt-in, keyed on raw SQL + identity + snapshot; disabled by default (`cache.rewrite.enabled`).
- **`sluice policy test`** — declarative YAML policy test suites (`internal/policytest`).
- **Human-approval workflow** — `ApprovalPolicy` kind, in-memory broker with webhook callbacks (accept/reject capability URLs), hybrid wait-then-pending flow (`ERR_APPROVAL_PENDING`/`REJECTED`/`EXPIRED`), single-use grants, MCP `check_approval`/`await_approval` tools, `GET /admin/approvals`, and the `[approval]` config section.
- **PolicyEngine plurality** — `policies.engine: yaml|opa|composite`; embedded OPA engine (`internal/opaengine`), ReBAC engine (`internal/rebac`) with an OpenFGA check client and the `RelationshipPolicy` kind.
- **Tag-driven policy** — `DataClassification` kind assigns tags; `ResourceSelector.tags` are expanded at compile time.
- **Per-subject daily budgets** — `internal/budget` (SQLite-backed) enforces `SubjectBinding.spec.budget` (CPU-seconds + rows) → `ERR_BUDGET_EXCEEDED`; `[budget]` config section and `sluice budget show` CLI.
- `SECURITY.md` with vulnerability disclosure process, response SLA, and operator hardening checklist.
- `CODE_OF_CONDUCT.md` (Contributor Covenant 2.1).
- `GOVERNANCE.md`, `ROADMAP.md`, `LICENSE-CC-BY`, `THREAT_MODEL.md`.
- `Dockerfile` — multi-stage, distroless `nonroot`, CGO-on for pg_query.
- `examples/hello-sluice/` — end-to-end demo (SQLite catalog, row filter, column mask, JWT).
- CI workflows: `security.yaml` (govulncheck + gosec + trivy) and `codeql.yaml` (weekly).
- **Prometheus endpoint** — `GET /metrics` is now mounted on the admin listener behind the admin bearer token (the handler existed but was never wired to a route).

### Changed

- **QueryRewritePolicy `hint` entries and out-of-subset CEL expressions now fail at policy load** (previously inert/rejected-as-unsupported).
- FPE ships **FF1**, not the roadmap's FF3-1 (FF3 was broken in 2017; FF1 is NIST-preferred and dependency-free).
- Embedded OPA adds ~17 MB to the binary; it is isolated behind the `PolicyEngine` interface.
- **Documentation overhaul** — README rewritten; `docs/` restructured around a new Architecture chapter (`docs/architecture/`) and a per-kind Policies chapter (`docs/policies/`, replacing `docs/concepts/`); narrative pages now use the real `sluice.bino.bi/v1alpha1` policy dialect and cover query rewrites, the full mask catalog, CEL, approvals, OPA, ReBAC, classification tags, budgets, and rate limits; new `reference/admin-api.md` and `operations/server-config.md`; `reference/policy-testing.md` moved to `policies/testing.md` (and into the nav).

### Fixed

- `examples/multi-tenant/policies.d/filter-tenant.yaml` used a schema shape that never loaded (`predicate:` directly under `spec:`, lowercase `op: equals`); corrected to `spec.filter.predicate` with `op: Equals`.
- Stale example READMEs (mask providers "landing in v0.2" that are shipped, four MCP tools instead of nine).

## [0.1.0] — _unreleased_

The v0.1.0 MVP cuts once the MVP Definition of Done passes. The line items below summarise what is on disk today and will roll into the tag once the release matrix is green on every target OS/arch.

### Added

Everything below landed between 2026-04-19 and 2026-04-20 across ten implementation slices.

#### Public API (Apache-2.0)

- `pkg/errors` — 19-code catalog, `APIError` with non-mutating builders, `errors.Is` by `Code`, ULID `NewQueryID`/`ParseQueryID`.
- `pkg/apitypes` — 8 Object kinds (`SqlAccessPolicy`, `RowFilterPolicy`, `ColumnMaskPolicy`, `QueryRejectPolicy`, `QueryRewritePolicy`, `DataSource`, `SubjectBinding`, `AuditSink`), `MaskArgs` with forward-compat `Extras`, `Duration` YAML/JSON, `CompileWildcard`, multi-doc Decoder with per-kind structural validation.
- `pkg/mask` — `Provider` interface, atomic.Pointer-backed `Registry`, `null` + `constant` providers, reflect-based mirror test against `apitypes.MaskArgs`.
- `pkg/datasource` — `DataSource` / `Schema` / `Table` / `Column` / `TableKey`, panic-on-duplicate Registry, `SecretResolver`, `AttachOptions`, `HealthOptions`, full `testfakes` subpackage.

#### Infrastructure (AGPL-3.0-or-later)

- `internal/version` — ldflag-populated `Build` struct, `Current()` memoised via `sync.Once`, `debug.ReadBuildInfo()` fallback, `PgQueryVersion()` atomic setter.
- `internal/telemetry` — slog JSON/text, Prometheus factories, `sluice_build_info` gauge (idempotent), `AttrError`, `Redacted{}` log value.
- `internal/secrets` — `secret://<provider>/<path>` URI, LRU cache with defensive copies, `env://` + `file://` providers, perm check rejects group/world writable, warns world-readable. Adapters satisfy `pkg/mask.SaltStore`, `pkg/mask.KeyStore`, `pkg/datasource.SecretResolver`.
- `internal/config` — `ServerConfig` with ten nested types + `DefaultServerConfig()`, viper wiring with `SLUICE_*` + `__`-to-`.` replacer, multi-doc YAML via `apitypes.Decode`, cross-file duplicate detection, default-deny on empty directory. `Registry` with atomic Pointer + `Subscribe`/`Publish`.
- `internal/config/watch` — fsnotify watcher (250 ms debounce, one-deep recursive registration), manual `Reload(ctx)` for SIGHUP / admin.

#### Query path

- `internal/parser` + `internal/pgquery` + `internal/parserbackend` — `pg_query_go/v6` default backend; build-tag-gated `pure_parser` stub (cockroachdb-parser deferred to v2). CTE-aware table walker, shape walker (`HasSelectStar`/`IsAggregate`/`HasCTE`/`HasUnion`/…), `StmtKind` whitelist, regex fallback. `FuzzParse` seeded.
- `internal/schema` — LRU cache (TTL 10 m, capacity 10 000), per-catalog singleflight, stale-serve on failure, `StaticLoader` for tests, Prom metrics (`sluice_schema_cache_{hits,misses}_total`, `_refresh_duration_seconds`, `_stale_entries`).
- `internal/datasource` — Registry with per-catalog `Status`, `AttachHook`, health ticker placeholder, `Probe` method. Six drivers: `sqlitefile`, `duckdbfile`, `postgres`, `mysql`, `s3parquet`, `motherduck`. Shared helpers under `drivers/common` (`ValidateIdentifier`, `EscapeSQLString`, `BuildCreateSecret`, S3 glob matching).
- `internal/identity` — `UserCtx` + private context key, `Composite` identifier, `APIKeyIdentifier` (HMAC-SHA256 + jitter), `JWTIdentifier` (alg allowlist HS/RS/ES 256/384, JWKS cache with per-URL singleflight + 30 s rate-limited refresh + stale-serve), `BindingRegistry` indexed by `iss`, JSONPath-lite claim extractor, HTTP middleware.
- `internal/executor` — hardened DuckDB pool (`external_access=false`, `allow_community_extensions=false`, `lock_configuration=true`), AttachHook-first init, `Request` → `Response{Columns, Rows, Truncated}`, `RowIterator` delegating `Scan` to `*sql.Rows`, per-request timeout + cancel.
- `internal/policy` — Engine with atomic snapshot swap; conflict resolver (deny-override → default-deny → row-filter combine → mask tiebreaker priority/specificity/name); templates `{{ subject.* }}` / `{{ request.* }}` render to `$N` params; CEL + QueryReject expressions declared-only. `FuzzTemplate` seeded.
- `internal/rewriter` — pg_query AST-level pipeline: deny/reject → statement-kind gate → pass-through → clone → expand_star (schema-cache driven) → inject_filter (wrap-as-subquery, alias preserved) → substitute_mask → deparse → fingerprint. 10 golden fixtures + `FuzzRewrite`.
- `internal/audit` — canonical JSON, SHA-256 hash chain, `GenesisPriorHash(seed)`, `FileSink` with daily + size rotation, `Dispatcher` (bounded channel + worker + flush), `Verify(dir, anchor)` walker, `VerifyError.Is(ErrChainBroken)`. `FuzzRecordCanonical` seeded.
- `internal/queryservice` — single orchestrator; `parse → policy → deny/reject | parse-error-if-allow → rewrite → execute → audited iterator`; exactly one audit record per terminal outcome; concurrency semaphore; `Explain`/`ListCatalogs`/`ListTables`/`DescribeTable` for transports.

#### Transports

- `internal/transport/rest` — stdlib `net/http.ServeMux` (Go 1.22+ method-scoped routes). `POST /v1/query` (JSON + CSV streaming), `GET /v1/health`, `GET /v1/ready`, `GET /v1/version`, `GET /openapi.json` (stub). Middleware: request-id, body cap, request timeout, panic recovery, identity. `X-Query-Id` on success + error.
- `internal/transport/mcp` — `modelcontextprotocol/go-sdk v1.5.0`; four tools (`execute_sql`, `list_catalogs`, `list_tables`, `describe_table`) via typed `AddTool[In, Out]`. Both stdio and Streamable HTTP. Errors returned as `CallToolResult{IsError: true}` so LLMs can self-correct.
- `internal/transport/admin` — separate port, static-token auth (constant-time compare). Read-only MVP endpoints: `/admin/{policies,datasources,subjects/explain,reload,audit/tail,healthz,version}`. `X-Admin-Request-Id` header on every response.

#### CLI

- `cmd/sluice serve` — 18-step composition root with signal handling (SIGINT/SIGTERM graceful shutdown, SIGHUP reload) and 10 s shutdown deadline.
- `cmd/sluice version [--json]`, `config validate`, `policy validate/explain`, `datasource check`, `audit verify`, `schema export [--kind]`. Exit codes 0/1/2/3/4.

#### Test infrastructure

- Golden rewrite harness under `internal/rewriter/testdata/rewrites/` with 10 starter fixtures and `-update` flag.
- Five fuzz targets: `FuzzParse`, `FuzzRecordCanonical`, `FuzzTemplate`, `FuzzRewrite`, `FuzzValidateArgs`.
- Benchmarks across parser, rewriter, policy, audit using Go 1.25 `b.Loop()`.
- `scripts/check-coverage.sh` — bash-3 compatible per-package threshold gate wired to `make coverage`.
- Makefile targets: `test-integration`, `test-fuzz`, `bench`, `coverage`.

#### Meta

- `LICENSE` (AGPL-3.0-or-later), `LICENSE-APACHE`, `NOTICE`, `README.md`, `CONTRIBUTING.md`, `AGENTS.md`.
- `Makefile` (`build`, `test`, `lint`, `fmt`, `vet`, `tidy`, `clean`, `spdx`, `all`).
- `.golangci.yaml` v2 — depguard + forbidigo enforced day one.
- `scripts/check-spdx.sh`.
- CI workflows: `test.yaml`, `lint.yaml`.
- `.goreleaser.yaml` skeleton (linux/amd64, CGO off — multi-arch upgrade pending).

### Security

- Every source file carries an SPDX header; `scripts/check-spdx.sh` enforces in CI.
- Default-deny: empty policy snapshot rejects every query (invariant covered by a test on every policy-touching slice).
- Templates never concatenate subject/request values into SQL — always flow through positional `$N` parameters.
- API-key failures add bounded random jitter so unknown-key-id vs bad-HMAC cannot be distinguished by response time.
- Admin port runs on its own listener and emits a loud warning if configured with an empty token.

---

[Unreleased]: https://github.com/bino-bi/sluice/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/bino-bi/sluice/releases/tag/v0.1.0
