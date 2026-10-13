# Final report: docs simplification

Base: commit 6f7b99f (`main`). Work: branch `docs/simplify`. The docs describe `main`.

## Acceptance criteria

| Criterion | Status | How it is checked |
|---|---|---|
| Top-level nav is Home, Getting started, Architecture, Policies, Operations, Security, Reference, Cookbook | Met | `scripts/docs-style-check.py` reads `mkdocs.yml` |
| The Compliance section is gone, no page links to it | Met | Style check, strict build |
| Security and the threat model are kept and list only protections in the code | Met | `docs-audit/security.md` has file and line for each row of the threat table |
| Every documented feature maps to code, nothing unimplemented is documented | Met | Audit files, plus six rounds of independent review against the code |
| Every code block comes from a tested file, `make docs-check` passes | Met, locally and in CI | Style check, `make docs-check`, job `docs-check` on pull request 11 |
| Every policy kind and every cookbook recipe has a runnable example with real output | Met | Tables under "Examples" |
| Every diagram is still present and renders | Met | Style check compares each diagram with its stored copy |
| `mkdocs build --strict` passes, no broken links | Met | Part of `make docs-check`. Link and heading checks are turned on in `mkdocs.yml` |
| The automated style check passes | Met | Part of `make docs-check` |
| Prose words are down by at least 40% | Met: 40% | Table below |
| The report lists what was removed, merged, and every mismatch | Met | This file |

## Prose words before and after

Counted with `scripts/docs-wordcount.py`. Code blocks are not counted. Tables are counted.

| Section | Pages before | Pages after | Words before | Words after | Change |
|---|---:|---:|---:|---:|---:|
| home | 1 | 1 | 328 | 180 | -45% |
| getting-started | 4 | 2 | 1728 | 652 | -62% |
| architecture | 4 | 3 | 4063 | 1365 | -66% |
| policies | 13 | 13 | 9887 | 6000 | -39% |
| operations | 6 | 6 | 3380 | 2020 | -40% |
| security | 5 | 5 | 3390 | 2576 | -24% |
| compliance | 4 | 0 | 1443 | 0 | -100% |
| reference | 9 | 10 | 4945 | 4980 | 1% |
| cookbook | 7 | 7 | 2670 | 1298 | -51% |
| **total** | 53 | 47 | 31834 | 19071 | **-40%** |

The target was at least 40% less. Reference stays the same size: its tables now list every working field, flag and key, and it holds the new glossary.

## Pages removed

| Page | Reason | Where its content went |
|---|---|---|
| `compliance/index.md`, `gdpr.md`, `bsi-c5.md`, `soc2.md` | Section deleted as decided | Two facts about real behavior moved: admin explain calls are audited as `admin-action` (`security/audit.md`), budget store content and retention (`policies/subjects.md`) |
| `getting-started/quickstart.md` | Merged | `getting-started/index.md` is now the Quickstart |
| `getting-started/first-query.md` | Merged | Steps in the Quickstart, request fields in `reference/rest-api.md`, `enforcementMode` in `policies/index.md` |
| `architecture/security-model.md` | 13 of 27 claims repeated the Security section | Fail-closed cases in `security/index.md`, JWT and API key facts in `policies/subjects.md` |

New page: `reference/glossary.md`.

## Content removed

- Go package paths, the package tour, parser and rewriter edge cases, cache key details and design history.
- Feature counts and marketing text.
- Text that repeated another page. Each fact now has one owner page. `docs-style.md` lists the owners.
- Every claim in the next table: the code does not do it, or a user cannot reach it.

### Documented before, not implemented

Line numbers refer to the pages at commit 6f7b99f.

| Old page | Line | Claim | What the audit found |
|---|---|---|---|
| `index.md` | 52 | `AuditSink` is one of the usable kinds | Looked for a reader of `Snapshot.AuditSinks` (internal/config/policies.go:131): none. Sinks come from server config (cmd/sluice/auditwire.go:28). s3, syslog, postgres, otlp types are refused (pkg/apitypes/decoder.go:516-521) |
| `getting-started/first-query.md` | 127 | Useful for trialing a new mask | Looked for the shadow match in the audit record, response headers and `policy explain`: none. `Decision.Shadow` (internal/policy/decision.go:35) has no reader outside internal/policy |
| `architecture/request-lifecycle.md` | 101-102 | Shadow entries are recorded in the audit record | Only `dec.Applied` is copied (`internal/queryservice/audit_helpers.go:97`). run: no shadow field in the record |
| `architecture/request-lifecycle.md` | 130 | PRAGMA is an allowed statement | On the allowlist (`internal/parser/shape.go:31`), but pg_query cannot parse it and `internal/pgquery/stmt.go:14-62` never returns `StmtPragma`. run: `ERR_SYNTAX` |
| `architecture/security-model.md` | 60-61 | `iat` is validated | Verifying parser has no `WithIssuedAt()` (`internal/identity/jwt.go:195-200`). The parser with it (`jwt.go:109-113`) is unused. run: `iat` one hour in the future is accepted |
| `architecture/security-model.md` | 96 | Log statements wrap sensitive values in `telemetry.Redacted` | Helper exists (`internal/telemetry/slog.go:100`), no call site outside tests |
| `policies/index.md` | 26 | `AuditSink`: where the audit trail is written | Decoded, never read: `internal/config/policies.go:131`, `cmd/sluice/auditwire.go:28` |
| `policies/matching.md` | 46 | `ipRanges`: remote IP inside a CIDR range | Never matches on REST: address has a port. Ran live |
| `policies/conditions.md` | 23-25 | `request.remote_ip`, `request.user_agent`, `request.headers` | Never filled on a live server. Ran live: all three empty |
| `policies/conditions.md` | 106-109 | Prefer `subjects.ipRanges` for network checks | `ipRanges` never matches, `request.remote_ip` is empty |
| `policies/row-filters.md` | 98 | `{{ request.remote_ip }}`, `{{ request.user_agent }}`, `{{ request.headers.<name> }}` | No request facts on a live server: HTTP 500 `ERR_INTERNAL` "request facts unavailable". Ran live |
| `policies/guardrails.md` | 17 | `expression` can use `request.*` | Always empty at run time. See summary. |
| `policies/guardrails.md` | 61 | Veto by "an IP range" | `ipRanges` never matches over REST. See summary. Owner: matching.md. |
| `policies/guardrails.md` | 78 | `PRAGMA` is skipped | Not parseable. See summary. |
| `policies/subjects.md` | 30-31 | `claims.tenantId`, `allowedRegions` map JWT claims | See summary. |
| `policies/subjects.md` | 31 | Any extra key becomes a named claim | See summary. Both modes. |
| `policies/subjects.md` | 59 | API-key mode: extra keys are stamped | See summary. Run. |
| `policies/subjects.md` | 101-102 | Recipe: JWKS, claims for selectors and tenant templates | The `tenantId: $.tenant` line has no effect. `groups` works. |
| `policies/opa.md` | 54 | Input `request` with remote IP, user agent, headers | Always empty. See summary. |
| `operations/index.md` | 16 | `datasources.directory` (`./datasources.d`) as a separate directory | Key never read. Looked for any use of `DataSources.Directory` in `cmd/` and `internal/`. |
| `operations/deployment.md` | 15 | There is no Helm chart | No `Chart.yaml`, no Kubernetes manifests, no unit file in the repo. |
| `operations/server-config.md` | 42 | `datasources.directory: ./datasources.d` | Never read. |
| `operations/server-config.md` | 144-146 | `#fragment` selects a subfield of a JSON secret | Parsed, never used. |
| `operations/server-config.md` | 148-150 | `secret://vault`, `aws-sm`, `gcp-sm` | Rejected at parse. hand: `config validate` exit 3 for all three in server config fields. |
| `operations/data-sources.md` | 11-12 | Dedicated `datasources.directory` | Never read. |
| `operations/data-sources.md` | 167 | `schemas` and `tables` restrict what the catalog exposes | hand, see summary. |
| `operations/data-sources.md` | 168 | `attachMode`: `readonly` or `readwrite` (future) | No driver reads it. |
| `operations/data-sources.md` | 169 | `healthCheck` `{query, interval}`, 30 s loop, `-1` disables | hand, see summary. Only the fixed 30 s loop is real. |
| `operations/hot-reload.md` | 106-108 | No Prometheus counter for reloads | Correct that none exists. Drop the sentence. |
| `operations/observability.md` | 21-23 | Secrets are wrapped in `telemetry.Redacted`, guarantee for logs, panics, metrics, audit | Type exists, no call site outside tests. |
| `operations/observability.md` | 62-63 | Catalog holds policy evaluations, denials, schema cache | Declared, never registered in `serve`. hand. The reference page lists them too. |
| `operations/observability.md` | 65-66 | Alert on rising `sluice_policy_denials_total` | Never exported. |
| `operations/observability.md` | 142-143 | otelsql spans are on the roadmap | No such dependency. |
| `operations/observability.md` | 147-149 | `postgres` and `otlp` audit sinks are not implemented | Rejected at decode. |
| `security/audit.md` | 27-28 | Event types `policy-decision`, `config-reload`, `auth-event`, `datasource-connect` | No emitter |
| `security/audit.md` | 127-128 | An `AuditSink` manifest records sink intent | Never read by serve |
| `security/audit.md` | 143-146 | `postgres` and `otlp` sinks | Page says so |
| `security/hardening.md` | 57 | Certificate identity is on the roadmap |  |
| `reference/index.md` | 13-14 | Hand edits make `make docs-check` fail in CI | Target works locally (run, exit 0). No workflow runs it. |
| `reference/rest-api.md` | 31 | `params` not supported, `$n` refused with `ERR_UNSUPPORTED_SYNTAX` | Refusal at internal/queryservice/execute.go:155 (run). |
| `reference/rest-api.md` | 77-80 | Arrow output is declared but rejected | rest/query.go:98, :113 (run). |
| `reference/rest-api.md` | 126, 128-131 | `/openapi.json` returns a stub; a generated spec is planned | Stub at rest/version.go:36-50 lists 4 paths, no approval paths (run). |
| `reference/policy-schema.md` | 69-71 | `healthCheck`, `healthCheck.query`, `healthCheck.interval` | No reader of `Spec.HealthCheck`. |
| `reference/policy-schema.md` | 91 | `groupsSource` | No reader. |
| `reference/policy-schema.md` | 220 | `rewrite.hint` | Refused at compile. (run) |
| `reference/policy-schema.md` | 43, 272-293 | `AuditSink` and 16 rows | Decoded, never used. See summary. |
| `reference/policy-schema.md` | 303-304 | Drift gate in `docs-deploy.yaml` fails on difference | See summary. |
| `reference/metrics.md` | 39 | `sluice_audit_rotate_duration_seconds` histogram, `sink` | Never observed. |
| `reference/metrics.md` | 46 | `sluice_policy_denials_total` | `RegisterMetrics` never called. (run) absent. |
| `reference/metrics.md` | 47 | `sluice_policy_eval_duration_seconds` | Same. |
| `reference/metrics.md` | 48 | `sluice_policy_evaluations_total` | Same. |
| `reference/metrics.md` | 49 | `sluice_policy_rejections_total` | Same. |
| `reference/metrics.md` | 61 | `sluice_schema_cache_hits_total` | `RegisterMetrics` never called. (run) absent. |
| `reference/metrics.md` | 62 | `sluice_schema_cache_misses_total` | Same. |
| `reference/metrics.md` | 63 | `sluice_schema_refresh_duration_seconds` | Same. |
| `reference/metrics.md` | 64 | `sluice_schema_stale_entries` | Same. |
| `reference/metrics.md` | 75-76 | Drift gate in `docs-deploy.yaml` | See summary. |
| `reference/configuration.md` | 45 | `datasources.directory` default `./datasources.d` | Never read. (run) |
| `reference/configuration.md` | 47 | `datasources.reload` default `false` | `true` is rejected. (run) |
| `reference/configuration.md` | 111-112 | Vault, AWS SM, GCP SM come in v0.3+; rejected until then | Rejection (run). |
| `reference/configuration.md` | 127-128 | Drift gate in `docs-deploy.yaml` | See summary. |
| `cookbook/mcp-agents.md` | 124-126 | Schema descriptions from a metadata store are not part of the tools | The page says so itself. Looked at tool list and `describe_table` output |

## Docs and code disagreed

156 claims were stated wrong. The new pages state what the code does. Line numbers refer to the pages at commit 6f7b99f.

| Old page | Line | Docs said | Code does |
|---|---|---|---|
| `index.md` | 5 | Each request, allowed or denied, lands in a hash-chained audit log, verifiable offline | Only requests that reach the pipeline. Run: 401, bad JSON and `format: arrow` left no record (internal/transport/rest/query.go:29-46). Verify works (cmd/sluice/audit.go:75) |
| `index.md` | 52 | 11 policy kinds | Doc: 11. Code: 11 decode (pkg/apitypes/typemeta.go:21-31), 10 have an effect. Count goes stale |
| `index.md` | 62 | 6 datasource drivers | 7 registered, `http` missing (cmd/sluice/bootstrap.go:17-23). `s3` is not Parquet only (pkg/apitypes/datasource.go:70-73) |
| `getting-started/index.md` | 21 | Compose file builds the image from the repository root and wires all secrets | Doc: builds. Code: pulls `ghcr.io/bino-bi/sluice:0.1.0-rc.2`, no `build:` key (examples/hello-sluice/docker-compose.yaml:10). Secrets part is right (:17-22) |
| `getting-started/install.md` | 91 | `make libduckdb`: 20 to 40 min, needs cmake, ninja, python3, cached under `third_party/` | Doc lists 3 tools. Script also needs git, C++ toolchain, curl, zip, unzip, tar, pkg-config (scripts/build-libduckdb.sh:40-42). Time and cache are right (Makefile:21 and :27) |
| `getting-started/install.md` | 123 | Any setting can come from env with `SLUICE_` and `__` | Only keys with a registered default (internal/config/server.go:420-495) or present in the config file. Run: `SLUICE_ADMIN__TOKEN` and `SLUICE_AUDIT__FILE__PATH` are ignored unless the file holds the key. `SLUICE_REST__LISTEN` always works |
| `getting-started/install.md` | 124 | One directory for policies, `DataSource`, `SubjectBinding`, `AuditSink`, hot-reloaded on change | Policies and bindings reload (run). `DataSource` changes need a restart (cmd/sluice/bootstrap.go:155, internal/config/validate.go:36-38). `AuditSink` is not wired |
| `getting-started/quickstart.md` | 9 | `docker-compose.yaml` builds the image | Doc: builds. Code: pulls the published image (examples/hello-sluice/docker-compose.yaml:10) |
| `getting-started/quickstart.md` | 18 | `data/` directory in the example | Not in the repo. Only ignore rules exist (examples/hello-sluice/.gitignore:3-5) |
| `getting-started/first-query.md` | 20 | Two headers on every response | `X-Sluice-Applied-Policies` only on 200 with applied policies (internal/transport/rest/query.go:65-68). `X-Query-Id` only when a query id exists (internal/transport/rest/errors.go:33-35). Run: 400 arrow and 401 have neither |
| `architecture/index.md` | 8-9 | Audit record for every request, allowed or denied | No record for 401 (run), transport 429 (`internal/transport/rest/ratelimit.go:27-30`), saturated slot (`execute.go:51-53`) |
| `architecture/index.md` | 41-42 | Six datasource types, attached as read-only catalogs | Seven types (`pkg/apitypes/datasource.go:58-64`). `s3`, `http` are in-memory catalogs with views (`internal/datasource/drivers/s3/driver.go:212`) |
| `architecture/index.md` | 59 | `pkg/errors` has the 24 canonical codes | 23 (`pkg/errors/codes.go:15-52`) |
| `architecture/index.md` | 90-91 | Query is not served unless the audit record is durably enqueued first | Gate exists (`internal/queryservice/audit_helpers.go:212-225`), default on (`internal/config/server.go:334`). "Durably" is wrong: in-memory queue (`internal/audit/dispatcher.go:203-210`) |
| `architecture/index.md` | 92-93 | Rewriter and executor allow only SELECT, EXPLAIN, SET, SHOW, PRAGMA. Writes rejected. Datasources READ_ONLY | Gate only in the rewriter (`internal/rewriter/rewriter.go:353-369`). PRAGMA never parses (see request-lifecycle row 30b). `s3`, `http` not READ_ONLY |
| `architecture/request-lifecycle.md` | 93 | `cache.rewrite.enabled: true` memoises decision and rewrite in an LRU | Only with engine `yaml` (`internal/queryservice/cache.go:18-21`, `internal/policy/engine.go:61`). run for `composite`. Default off, size 4096, TTL 60 s (`internal/config/server.go:349-353`) |
| `architecture/request-lifecycle.md` | 130-132 | Only SELECT, EXPLAIN, SET, SHOW are allowed. Writes give `ACL_REJECTED`. DDL, COPY, ATTACH give `ERR_UNSUPPORTED_SYNTAX` | Gate: `internal/parser/shape.go:29-36`, `internal/rewriter/rewriter.go:353-369`. run: writes `ACL_REJECTED`, DDL and COPY `ERR_UNSUPPORTED_SYNTAX`, ATTACH `ERR_SYNTAX`. Only when policy allows, else `ACL_DENIED`. `SHOW tables` works, `SET` see summary |
| `architecture/request-lifecycle.md` | 179-182 | Access record must be durably enqueued before the first row, else `ERR_AUDIT_UNAVAILABLE`. `audit.failClosed: false` serves anyway | Gate and flag are right (`execute.go:386-395`, `audit_helpers.go:212-225`). "Durably" is wrong, in-memory queue, deadline 200 ms (`internal/audit/dispatcher.go:78`, `:203-229`). Duplicate of `security/audit.md` |
| `architecture/request-lifecycle.md` | 182-184 | Close writes a completion record with the row count and records budget usage as execution CPU time | Record and budget write are right (`audit_helpers.go:289-324`). Time is wall-clock from request start to executor return (`execute.go:421`) |
| `architecture/request-lifecycle.md` | 196 | Table: Policy codes | Also `ERR_INTERNAL` on an engine error (`execute.go:203-209`), run for OPA and ReBAC |
| `architecture/request-lifecycle.md` | 197 | Table: Approval codes | Also `ERR_INTERNAL` when no broker is configured (`execute.go:252-260`) |
| `architecture/request-lifecycle.md` | 198 | Table: Rewrite codes | List is right, but ATTACH is not there (see row 30) and `ERR_INTERNAL` also comes from a missing template variable (run) |
| `architecture/request-lifecycle.md` | 199 | Table: Execute codes | `ERR_DATASOURCE_UNAVAILABLE` is missing (`execute.go:584-585`) |
| `architecture/policy-engines.md` | 31-39 | Every engine gives the same decision shape (outcome, filters, masks, rewrite effects, rejections, approval, applied names) | Go type is shared (`internal/policy/decision.go:25-50`). OPA cannot return rewrite effects or approval (`internal/opaengine/contract.go:78-85`). ReBAC returns deny or abstain only |
| `architecture/policy-engines.md` | 41-43 | `sluice policy test` asserts exactly this contract, a YAML suite describes any engine | YAML engine only (`internal/policytest/policytest.go:106`). No assertion for approval, row cap, sample, timeout (`policytest.go:69-78`) |
| `architecture/policy-engines.md` | 107-109 | Same snapshot, subject, query always give the same decision. No clock variable | True for YAML and CEL (`internal/policy/cel.go:18`, `:33-35`). Not for ReBAC |
| `architecture/security-model.md` | 12-14 | Only SELECT, EXPLAIN, SET, SHOW, PRAGMA. Writes `ACL_REJECTED` "write operations are not permitted". DDL, COPY, ATTACH `ERR_UNSUPPORTED_SYNTAX` | Same as request-lifecycle rows 30 and 30b. Message text is right (`internal/rewriter/rewriter.go:359-360`, run). Also in `security/index.md:11-13`, `security/threat-model.md:63-64` |
| `architecture/security-model.md` | 41-43 | CEL condition error or a template variable the subject lacks denies the request | CEL: deny (`internal/policy/engine.go:112-126`). Template: `ERR_INTERNAL`, HTTP 500 (run). No row is served in both cases |
| `architecture/security-model.md` | 46-47 | OpenFGA error fails the request, errors are not cached | Heading says deny. Code gives `ERR_INTERNAL`, HTTP 500 (run). Cache part is right (`internal/rebac/engine.go:203-207`). Owner `policy-engines.md:85-86` |
| `architecture/security-model.md` | 67-70 | Credentials are `secret://` refs (`credentialsRef`, `tokenRef`, schemes `env`, `file`), injected with `CREATE SECRET` | postgres, mysql, s3, http use `CREATE SECRET` (`internal/datasource/drivers/postgres/driver.go:148-161`). MotherDuck `tokenRef` uses `SET motherduck_token` (`internal/datasource/drivers/motherduck/driver.go:108`). Owner `operations/data-sources.md` |
| `architecture/security-model.md` | 70-72 | Every catalog is attached `READ_ONLY` | Not `s3`, `http` (see index row 5) |
| `policies/index.md` | 38, 54-55 | `metadata.name` is an RFC 1123 label, at most 63 characters | Pattern checked, length not: `decoder.go:222`, `262`. Ran: 72 characters load |
| `policies/index.md` | 62-68 | Every enforcement kind has `match`, `exclude`, `conditions`, `enforcementMode` | `RelationshipPolicy` has no `conditions`: `policy_relationship.go:17-19` |
| `policies/index.md` | 72 | Shadow policies never change the decision | A shadow policy with a condition that errors denies the request: `engine.go:166-200`. Ran live: 403 `ACL_DENIED` |
| `policies/index.md` | 157-158 | `--strict` rejects unknown YAML fields | Unknown keys in `spec.mask.args` pass: `mask_args_json.go:78-91`. Ran `prioritty` (rejected) and `shwLast` (accepted) |
| `policies/matching.md` | 8 | Empty selector matches nothing, the policy is inert | Empty `spec.match` is a load error: `decoder.go:541-549`. Only an empty `exclude` matches nothing. Ran |
| `policies/matching.md` | 44 | `apiKeys`: API key whose id is listed | Compares the subject id: `selector.go:436-440`. Ran live: key id `sl_demo_hello` no match, subject `hello` match |
| `policies/matching.md` | 104-106 | Same kind, same target: priority, then specificity, then name. Specificity counts non-wildcard patterns | Specificity is for masks only: `conflict.go:295-303`. Count is catalogs, schemas, tables, columns and tags, best clause wins: `selector.go:216-217`, `370-386` |
| `policies/conditions.md` | 30-31 | `subject.auth_method` is `jwt`, `api_key`, `admin_token` or `none` | Only `jwt` and `api_key` are set. `admin_token`, `none` are declared only: `internal/identity/userctx.go:20-21`. Ran live: `api_key` |
| `policies/conditions.md` | 143-145 | Without the claim the template cannot render and the request is denied | HTTP 500 `ERR_INTERNAL`: `inject_filter.go:297`, `rewriter/errors.go:77`. Ran live |
| `policies/testing.md` | 6-8 | Runs the same parse, evaluate, rewrite steps as the server, so a passing suite reflects real enforcement | Steps are the same (`internal/policytest/policytest.go:260-299`). The conclusion is not: see "Runner reference" below |
| `policies/testing.md` | 105-107 | Exit 0 pass, 1 I/O or flag error, 3 compile failure, case failure or zero cases | Compile failure is exit 1: `cmd/sluice/policytest.go:53-63`. Ran. The rest is right |
| `policies/access-control.md` | 8-10 | Default deny, also with an empty directory. Cannot be configured away | True for the YAML engine (`conflict.go:145-153`). `policies.engine: opa` or `composite` changes it: `cmd/sluice/policyengine.go:23-46`. Read only |
| `policies/access-control.md` | 107-108 | The key id comes from `SubjectBinding` `apiKeys[].id` | The selector compares the subject id. See matching.md claim 5 |
| `policies/row-filters.md` | 49 | `IsNull`, `IsNotNull` as `col IS NULL`, `col IS NOT NULL` | Emits `col OPERATOR(IS) NULL`: `inject_filter.go:419-434`. DuckDB parser error, HTTP 500 `ERR_INTERNAL`. Ran live |
| `policies/row-filters.md` | 85 | The example injects `WHERE deleted_at IS NULL AND (...)` | Real: `WHERE sluice_src.deleted_at OPERATOR(IS) NULL AND (sluice_src.region IN ($1, $2) OR sluice_src.vip = $3) AND NOT sluice_src.status = $4` |
| `policies/row-filters.md` | 94 | `{{ subject.groups }}` | Gives a list, DuckDB cannot bind it: HTTP 500 `ERR_INTERNAL`. Ran live |
| `policies/row-filters.md` | 101-103 | Missing variable: the request is denied | HTTP 500 `ERR_INTERNAL`, not a deny. Ran live |
| `policies/row-filters.md` | 137-138 | "the tenant filter from the policy model (index.md)" | index.md has no row filter |
| `policies/column-masks.md` | 143-144 | Args are validated at load and again at rewrite | Once, at load: `snapshot.go:521-533`. Bad args do fail `policy validate`. Unknown arg keys are accepted |
| `policies/guardrails.md` | 16 | `name` identifies the rule in the error and the audit record | Not in the error body, not in the audit record. See summary. |
| `policies/guardrails.md` | 21 | The variables describe the parsed statement | Top-level SELECT and left arm of a set operation only. See summary. |
| `policies/guardrails.md` | 77 | A parameter limit is replaced by the cap | `$n` is refused. See summary. |
| `policies/approvals.md` | 47-48 | `reason` is in the status of `GET /v1/approvals/{id}` and `check_approval` | Not returned. See summary. |
| `policies/approvals.md` | 51-54 | Unparseable query with non-empty `when` is parked | Returns `ERR_SYNTAX`, nothing is parked. See summary. |
| `policies/approvals.md` | 129 | Grant timed out: `ERR_APPROVAL_EXPIRED`, 410 | The re-run parks again with 202. See summary. |
| `policies/approvals.md` | 160 | The shadow is visible in `sluice policy explain` | Only with `--json`. See summary. |
| `policies/subjects.md` | 16 | Validates `iat` | Not checked. See summary. |
| `policies/subjects.md` | 36-38 | Extracted values flow into selectors, templates and CEL | Groups do. Templates and `subject.claims` read the raw token claims (internal/policy/template.go:113-118, internal/policy/cel.go:118-137), not mapped names. |
| `policies/subjects.md` | 66 | An API-key binding limits its subject | Only with `claims.subjectId` or `spec.issuer`. See summary. |
| `policies/subjects.md` | 67-68 | `rps: 0` or no block: unlimited | `limits.defaultSubjectRps` applies. See summary. |
| `policies/subjects.md` | 68-69 | Anonymous requests are bounded by `limits.maxConcurrent` | REST has no anonymous requests. See summary. |
| `policies/subjects.md` | 70 | Hot-reloadable, a changed spec resets the bucket | Reload works (cmd/sluice/bootstrap.go:481-484). Every reload resets all buckets. |
| `policies/subjects.md` | 75 | Usage is CPU milliseconds and rows | Wall-clock time, not CPU. See summary. |
| `policies/subjects.md` | 92 | `failClosed`: store unavailable refuses queries | Startup only. See summary. |
| `policies/subjects.md` | 102 | Recipe: "generous shared rate limit" | One bucket per subject. See summary. |
| `policies/rebac.md` | 62 | Only backend type `openfga` is supported | `type` is never read. See summary. |
| `policies/rebac.md` | 80-81, 114-116 | Shadow matches show would-be denials | Same entry for pass and fail. See summary. |
| `policies/opa.md` | 28-29 | Broken modules fail the reload, old modules stay | Old modules stay. The reload reports success. See summary. |
| `policies/opa.md` | 29-30 | `engine: opa` with no modules: every query fails | The server does not start. See summary. |
| `operations/server-config.md` | 6-9 | Page shows every top-level block with defaults | `tracing` block missing (`server.go:28,329-332`). Ten keys missing, see summary. |
| `operations/server-config.md` | 109-110 | Precedence: defaults, file, `SLUICE_*`, command-line flags | Env over file over defaults is right (hand). Flags: only `--policies-dir` (`bootstrap.go:93,130-133`). |
| `operations/server-config.md` | 151-152 | Boot or reload fails for manifest refs to these providers | hand: validate exit 0, reload applied with a WARN for `hashRef`. See summary. |
| `operations/server-config.md` | 161 | `spec.jwt.hmacSecretRef` on SubjectBinding | Code: `spec.hmacSecretRef` (`pkg/apitypes/subjectbinding.go:18`). |
| `operations/server-config.md` | 168-172 | Cache cleared on reload only when API-key auth is configured | Cleared on every reload (`bootstrap.go:446-450`). |
| `operations/server-config.md` | 180-181 | `--strict` rejects unknown YAML fields | Only in manifests (hand: exit 3 for a misspelled manifest field). Unknown keys in `sluice.yaml` pass (hand: exit 0). |
| `operations/data-sources.md` | 192 | Released `darwin/windows/linux-arm64` binaries | No windows release (`.goreleaser.yaml:36-43`). |
| `operations/data-sources.md` | 200-205 | Statement allowlist and the error codes for writes, `COPY`, DDL, `ATTACH`, `LOAD`, `INSTALL` | hand, see summary. This rule set needs one owner page outside Operations. |
| `operations/data-sources.md` | 215 | Exit `2` when any source fails | Exit 1 for invalid manifests, 2 only for "no DataSource manifests found". hand. |
| `operations/data-sources.md` | 220-223 | A dead source turns unhealthy within one interval, 503, `ERR_DATASOURCE_UNAVAILABLE` | hand with SQLite, see summary. |
| `operations/hot-reload.md` | 30-32 | A snapshot that fails compilation is rejected before publish on every path, no half-applied state | True for YAML policy compile only (`bootstrap.go:402-405`). See summary. |
| `operations/hot-reload.md` | 34 | Watcher covers the directory tree recursively | Only directories present at start. hand. |
| `operations/hot-reload.md` | 78-79 | Without a base URL, a reload-added ApprovalPolicy logs an error and matching queries pend until expiry | Error logged, queries fail at once with `ERR_INTERNAL`. hand. |
| `operations/observability.md` | 69-72 | Drop labels: s3 `buffer_full` and `write_error`, syslog `write_error` | Also `queue_full` and `context` for every sink (`internal/audit/dispatcher.go:224-236`). The named ones are in `s3_sink.go:141,211`, `syslog_sink.go:151`. |
| `security/index.md` | 11-12 | Only SELECT, EXPLAIN, SET, SHOW, PRAGMA reach the engine | PRAGMA never parses: 400 `ERR_SYNTAX`. SET is refused by DuckDB. See summary |
| `security/index.md` | 16-17 | Query not served unless the access record is "durably enqueued" | Enqueued in memory, written to disk up to 200 ms later. See summary |
| `security/index.md` | 21 | Credentials are `secret://` references, never inlined | `admin.token` is an inline string (internal/config/server.go:70) |
| `security/audit.md` | 5-8 | Every terminal outcome appends at least one record | No record for 401, transport 429, `service saturated`, admin calls except explain. Deny, reject, error and allow paths do write (internal/queryservice/execute.go:216-231, audit_helpers.go:196, 289-318). Ran |
| `security/audit.md` | 17 | `subject`: id, method (jwt, api_key, admin_token, none), issuer, email, groups, remote_ip | Only `jwt` and `api_key` are set. `remote_ip` is `host:port`. See summary |
| `security/audit.md` | 22 | `row_count`, `truncated`, `duration_ms` only on `query-result` | `duration_ms` is on every query record. `row_count` and `truncated` are left out when 0 or false |
| `security/audit.md` | 24 | `sluice_version`, `duckdb_version`, `parser_version` | `duckdb_version` is never set. `parser_version` on genesis only |
| `security/audit.md` | 183-184 | An unreachable daemon fails boot | True for tcp and unix only |
| `security/audit.md` | 208 | Copy files away before local rotation removes them | Rotation never removes files |
| `security/threat-model.md` | 64 | DDL, COPY, ATTACH: `ERR_UNSUPPORTED_SYNTAX` | ATTACH gives `ERR_SYNTAX`. DDL without an allowed table gives `ACL_DENIED`. See summary |
| `security/threat-model.md` | 76 | `ApprovalPolicy` with single-use capability URLs, uniform 404 | URL is replayable, the grant is single-use. 404 part is right (internal/transport/rest/approvals.go:41). Ran the full flow |
| `security/threat-model.md` | 93-97 | Missing filter column lets a crafted query read unfiltered rows | Not reproduced, see summary. True part: no load-time check |
| `security/hardening.md` | 63-64 | Pass the admin token as `SLUICE_ADMIN__TOKEN` instead of writing it into the file | Ignored when the YAML has no `admin.token` key. See summary |
| `security/hardening.md` | 70-74 | `s3` `allowedPaths` and `http` URLs are the DuckDB external access carve-out | S3 carve-out is the whole bucket |
| `security/hardening.md` | 92-93 | Rate limit and budget on every agent binding | The example enforces neither. See summary |
| `security/hardening.md` | 148-151 | DryRun first, watch the audit log for would-be denials | The audit log has no trace of DryRun matches |
| `security/key-rotation.md` | 13, 75-78 | HS secret: restart, resolved once at startup, hard cutover | Re-resolved on every policy reload. Ran |
| `security/key-rotation.md` | 14, 82, 86-88 | Genesis seed: restart starts a new chain, a mixed directory will not verify | The seed is used only for an empty directory. Ran |
| `security/key-rotation.md` | 16 | Admin token: `admin.token` or `SLUICE_ADMIN__TOKEN`, restart | The env var alone does not set the token. YAML key plus restart is right (cmd/sluice/bootstrap.go:418-422) |
| `reference/index.md` | 23 | Policy schema page: JSON Schema 2020-12 for all 11 kinds | Page holds Go struct field tables. `schema export` union fails (run). |
| `reference/cli.md` | 5-6 | `serve` is the composition root, all other commands are self-contained and read-only | `mcp` builds the runtime and runs queries (cmd/sluice/mcp.go:73). `budget show` creates `budget.db` (cmd/sluice/budget.go:37, run). |
| `reference/cli.md` | 15-17 | Every server.yaml field has a `SLUICE_` override, `.` maps to `__` | See summary. Owner: configuration.md. |
| `reference/cli.md` | 89-92 | `config validate` flags; `--strict` rejects unknown YAML fields | Only for manifests (cmd/sluice/config.go:67-69). Unknown server.yaml key passes (run). |
| `reference/cli.md` | 193-195 | `schema export` prints JSON Schema 2020-12, oneOf union of 11 kinds or one kind | Union and `--kind RowFilterPolicy` exit 1 (run). See summary. |
| `reference/rest-api.md` | 30 | `sql` string, required | No check. `{}` gives 403 `ACL_DENIED` (run). |
| `reference/rest-api.md` | 135-137 | Every 4xx/5xx from query and meta endpoints is a JSON `APIError` | 404 and 405 from the router are plain text (run). |
| `reference/admin-api.md` | 24-28 | TLS on the admin listener is not implemented; `admin.tls` fails validation | Works. See summary. Tested: admin/tls_test.go:16, internal/config/validate_test.go:69. |
| `reference/admin-api.md` | 48-49 | `/admin/datasources`: list of `name, type, healthy, last_check, last_error, latency_ms, schema_pulled_at` | Wrapped in `{"datasources":[...]}`; two fields are optional. (run) |
| `reference/admin-api.md` | 67, 78 | `simulated_sql` is evaluated instead of the bare table | Table is still required and added. (run) |
| `reference/admin-api.md` | 131 | "Configure the scrape with a credentials file" | The YAML uses inline `credentials`. |
| `reference/mcp.md` | 42 | `row_limit` range 1..100000 | Not enforced. See summary. |
| `reference/mcp.md` | 58 | `list_tables` returns the tables of the catalog | Only tables in the schema cache. Empty on a fresh server. (run) |
| `reference/mcp.md` | 83-85 | `list_accessible_tables`: `catalog`, `limit`, `cursor`; tables the identity may query | Same cache limit as row 15. (run) |
| `reference/mcp.md` | 103-109 | Only an explicit Deny hides a table; reject rules and approval gates still list | Any deny outcome hides, default-deny included. (run) |
| `reference/mcp.md` | 144-146 | Runnable `sluice serve` variant in `examples/mcp-agent/claude-desktop.json` | `serve` exits 1 with that example. (run) |
| `reference/policy-schema.md` | 6-7 | Policies are YAML validated against a JSON Schema from `pkg/apitypes` | Go struct decode plus Go checks (pkg/apitypes/decoder.go:162, :216). |
| `reference/policy-schema.md` | 7-8 | The schema is bundled in the binary | Built at run time (cmd/sluice/schema.go:65). |
| `reference/policy-schema.md` | 8-9 | `sluice schema export` emits a oneOf union; `--kind` narrows | Union exits 1. Owner: cli.md. (run) |
| `reference/policy-schema.md` | 16 | `apiVersion: sluice.bino.bi/v1alpha1` for every kind | Two kinds need v1beta1. (run) |
| `reference/policy-schema.md` | 65 | `readonly` is required | Omitting it passes (run). |
| `reference/policy-schema.md` | 139-140 | `filter.predicate.value`, `.values` with empty type | Type is `any` / list of `any`. Generator bug. |
| `reference/policy-schema.md` | 161 | `mask.args.value` with empty type | Type is `any`. |
| `reference/policy-schema.md` | all tables | Column "Notes": required / optional | From `omitempty` only. See summary. |
| `reference/metrics.md` | 7-8 | Names follow `sluice_<subsystem>_<metric>` with `_total` / `_seconds` / `_bytes` | No `_bytes` metric. `sluice_schema_stale_entries` is a counter without `_total`. |
| `reference/error-codes.md` | 7-8 | Every API response, CLI message and audit record carries one of these codes | See summary. |
| `reference/configuration.md` | 18-20 | Every field can be overridden through the environment | See summary. (run) |
| `reference/configuration.md` | 24-100 | Env var column | Computed by the script. Wrong for keys with no viper default and for struct or list keys. See summary. |
| `reference/configuration.md` | 104-105 | All `*Ref` / `genesis` / `token` fields accept `secret://<provider>/<path>` | Not `admin.token`. (run) |
| `reference/configuration.md` | 116-118 | `sluice schema export` covers every policy object | Union exits 1. Owner: cli.md. |
| `cookbook/index.md` | 5-6 | Each recipe maps 1:1 to a runnable directory under `examples/` | approval-workflow has only `policies.d/approval.yaml`, `run.sh`, README. mcp-agent exits 1 as shipped (`internal/config/validate.go:102`) |
| `cookbook/index.md` | 6-7 | The YAML on the page is the configuration you run | Drift in multi-tenant, break-glass, mcp-agents. Page-only policy in cross-source-joins |
| `cookbook/index.md` | 7-9 | Every recipe ends with `sluice policy test` or two curl calls | approval-workflow and mcp-agents have neither. break-glass and cross-source describe the test in prose only |
| `cookbook/index.md` | 13-20 | Policy kinds per recipe | Kinds exist (`pkg/apitypes/typemeta.go:21-28`). MCP row (rate limit, budget, QueryRewritePolicy) and cross-source row (QueryRejectPolicy) are not in the example directories |
| `cookbook/multi-tenant.md` | 19-65 | Policy YAML equals the example | Key ids `acme`, `beta` at lines 57 and 65. Example: `sl_demo_acme`, `sl_demo_beta`. Rest is equal |
| `cookbook/multi-tenant.md` | 76-78 | Compose hashes are placeholders, replace them | They are valid for `sl_demo_acme` and `sl_demo_beta` with material `supersecret` |
| `cookbook/pii-masking.md` | 109-110 | WHERE, GROUP BY and joins on a masked column see the masked value | True for `null`, `constant`, `partial` (verified). Post-query masks refuse it: `ERR_MASK_UNSUPPORTED_CONTEXT` 400 (`internal/queryservice/maskedrows.go:90`) |
| `cookbook/pii-masking.md` | 177-179 | `hash` default is `sha256`, in SQL, unkeyed | Default and SQL path correct. `sha256` accepts `saltRef` (`pkg/mask/hash.go:29-40`) |
| `cookbook/break-glass.md` | 20-66 | Policy YAML equals the example | Mask is equal. Binding names and key ids differ (lines 46, 51, 57, 63) |
| `cookbook/break-glass.md` | 97-98 | Both queries go to the same hash chain with subject and groups | Only the `query` record has the subject. `query-result` has `"subject":{}` |
| `cookbook/break-glass.md` | 120-121 | For access and reject kinds a matching exclude drops the policy for the whole query | For `SqlAccessPolicy` the exclude is applied per table unless it has an `all` block or covers every table (`internal/policy/engine.go:179-187`). Owner: policies/matching.md |
| `cookbook/approval-workflow.md` | 40-41 | `when` conditions are OR'd: `ssn`, `salary*` or `country = 'de'` | OR is correct (`internal/policy/approval.go:55-68`, verified). `SELECT *` returns `ssn` without approval. `IN ('de')` and `lower(country) = 'de'` do not trigger |
| `cookbook/approval-workflow.md` | 65-66 | `run.sh` drives the flow | Needs `jq` and a pasted URL. No server config, data or binding in the directory |
| `cookbook/approval-workflow.md` | 71-85 | Webhook payload fields | Fields exist (`internal/approval/webhook.go:51-61`). Real `subject` has no `email` key when empty, `issuer` is the binding name |
| `cookbook/approval-workflow.md` | 95-96 | Rejected gives `ERR_APPROVAL_REJECTED` 403, expired gives `ERR_APPROVAL_EXPIRED` 410 | Only inside the waiting request (`internal/queryservice/approval.go:91-102`). Verified 403 and 410 that way. A later re-run gives 202 |
| `cookbook/cross-source-joins.md` | 148 | "All six drivers" | Seven types (`pkg/apitypes/datasource.go:58-64`) |
| `cookbook/mcp-agents.md` | 16-34 | Claude Desktop config | Flags are real. Fails with the example `server.yaml` (`internal/config/validate.go:102`). Example file uses `serve` |
| `cookbook/mcp-agents.md` | 37 | Paths must be absolute | Example `server.yaml` and DataSource use `./data/...`, resolved against the working directory. From another directory: `stat .../data/shop.db: no such file`, exit 1 (verified) |
| `cookbook/mcp-agents.md` | 47-95 | Policy YAML equals the example | `QueryRewritePolicy/agent-guardrails` is page only. Binding `rateLimit` and `budget` are page only |
| `cookbook/mcp-agents.md` | 89-91 | `budget` rows and CPU seconds per day | Needs `budget.enabled: true` in `server.yaml` (`cmd/sluice/budgetwire.go:20`). Not said, not set in the example |
| `cookbook/mcp-agents.md` | 110-111 | `list_accessible_tables` gives `shop.main.products` | Empty until the catalog is in the schema cache (`internal/queryservice/tables.go:174`) |
| `cookbook/mcp-agents.md` | 121-122 | Every call lands in the audit log under the pinned subject | Only `execute_sql` and `explain_access` are audited |
| `cookbook/mcp-agents.md` | 133-136 | An empty table list is a policy gap | Also empty on a cold schema cache |


## Examples

36 example folders under `examples/`, 28 of them start a local server. 164 steps have golden output. 29 suites with 218 cases run with `sluice policy test`. `make docs-check` runs all of them in about 80 seconds.

Every code block in `docs/` is a snippet include of one of these files. The only exceptions are the four Mermaid diagrams and the ASCII pipeline on the home page. `scripts/docs-style-check.py` checks this: a block must include a step, `serve.sh`, a suite, a policy file that the loader reads, a golden file of a step, a manual file from its `MANUAL_FILES` list, or a file that a step, `serve.sh` or `server.yaml` names by its path. A policy, suite or Rego directory that a step, `serve.sh` or `server.yaml` names as a whole argument covers the YAML files directly in it, or the `.rego` files for `moduleDir`. The check does not know the kind of such a directory. The runner loads every `policies.d/` with `--strict`. It fails on a suite key that `sluice policy test` would ignore, in `tests/` and in a file or folder that a step passes to `--tests`. It fails on a key of a server config (`server*.yaml`, or a file that a step passes to `--config`) that is not a row of the generated configuration reference. Not covered: keys below `spec.mask.args`, which strict mode does not check, and policy files outside `policies.d/` that a step copies into the policy directory.

| Policy kind | Example with real output |
|---|---|
| `SqlAccessPolicy` | `access-control` |
| `RowFilterPolicy` | `row-filters`, `multi-tenant` |
| `ColumnMaskPolicy` | `column-masks`, `pii-masking` |
| `QueryRejectPolicy` | `guardrails` |
| `QueryRewritePolicy` | `guardrails`, `mcp-agent` |
| `ApprovalPolicy` | `approval-workflow` |
| `SubjectBinding` | `subjects`, `key-rotation` |
| `DataSource` | `cross-source-join`, `data-sources` |
| `DataClassification` | `classification` |
| `RelationshipPolicy` | `rebac` |

| Cookbook recipe | Example |
|---|---|
| Multi-tenant isolation | `multi-tenant` |
| PII masking | `pii-masking` |
| Break-glass access | `break-glass` |
| Approval workflow | `approval-workflow` |
| Cross-source joins | `cross-source-join` |
| MCP agents | `mcp-agent` |

Three examples did not run before and were rebuilt:

- `approval-workflow` had no server config and no data. It now has both, and a small webhook receiver.
- `cross-source-join` needed Postgres and S3. It now joins a SQLite file and a DuckDB file. The old variant is kept under `manual/postgres-s3/`.
- `mcp-agent` did not start. It now uses MCP over HTTP with `curl`.

Two examples use a stand-in for an outside service: `rebac` has a small stub of the OpenFGA check endpoint, and `approval-workflow` has the webhook receiver.

### Shown in the docs, not executed

The check cannot run these files. They get a syntax check only: `bash -n`, or a JSON, YAML or unit file parse.

| File | Why it is not run |
|---|---|
| `install/manual/build.sh`, `start-example.sh`, `second-terminal.sh` | `git clone`, `make build` and a server in the foreground |
| `install/manual/docker-example.sh`, `image.sh` | Docker and cosign |
| `install/manual/archive.sh` | Downloads a release archive, checks it with cosign |
| `deployment/manual/docker-compose.yaml`, `sluice.service` | Docker and systemd |
| `mcp-agent/manual/claude-desktop.json` | A config file for Claude Desktop. Its command line was run by hand |
| `cross-source-join/manual/postgres-s3/*` | Postgres and S3. The policy files pass `sluice policy validate` in a step |
| `examples/*/docker-compose.yaml` | Docker. Paths were changed to the new layout and not run |

The `DataSource` files for `postgres`, `mysql`, `s3`, `http` and `motherduck` in `examples/data-sources/` are validated by `sluice policy validate` and `sluice datasource check`. They are not connected to a service.

## Diagrams

All five diagrams of `docs-audit/diagrams.md` are present. The style check compares each with its copy in `docs-audit/diagrams/`.

One diagram changed, the component map in `architecture/index.md`. The maintainer approved the changes:

| Change | Reason |
|---|---|
| Node label `S3 Parquet` is now `S3` | The type is `s3` and reads parquet, csv, json and xlsx |
| New node `HTTP` | The `http` data source type had no node |
| Edge from Admin to identity removed | The admin listener checks a bearer token and does not use the identity layer |

## Reference pages: generated or checked by hand

| Page | Source |
|---|---|
| `error-codes.md` | Generated from `pkg/errors` |
| `policy-schema.md` | Kinds, fields, types, `apiVersion` and enum values are generated from `pkg/apitypes`. The "Required" and "Meaning" cells are short texts kept in the generator, checked by hand against the code. The value lists for `fakeType`, `alphabet` and the approval `op` are kept by hand |
| `configuration.md` | Keys and defaults are generated from `internal/config`. The "Meaning" cells and nine defaults of optional blocks are kept in the generator, checked by hand |
| `metrics.md` | Generated from the metric definitions. One row (`sluice_rest_rate_limited_total`) is kept by hand |
| `cli.md` | Generated from the `--help` output of the built binary by `scripts/gen-cli-docs.go`. Some help texts are reworded in the generator (its `overrides` map) to fit the style rules and the glossary. The exit code table is kept by hand, checked by running each command |
| `rest-api.md`, `admin-api.md`, `mcp.md` | Written by hand. Each route and tool was checked against the route setup in `internal/transport/` and called on a local server |

A generator stops with an error when a kept text names a field that no longer exists, or when a new field has no text. Each generator has a short skip list for things the code declares and never uses.

## Problems found in the code

The docs task did not change product code. These findings are for the maintainers. The new pages either leave the feature out or state the limit in one line.

| Area | Finding |
|---|---|
| Row filters | The ops `IsNull` and `IsNotNull` produce SQL that DuckDB rejects. Every query on the table returns 500 |
| Row filters | A template variable that the subject does not have returns 500 `ERR_INTERNAL`, not a deny. `{{ subject.groups }}` cannot be bound |
| Matching | `subjects.ipRanges` never matches on REST, because the remote address holds a port |
| Conditions | `request.remote_ip`, `request.user_agent` and `request.headers` are never filled |
| Policies | The result of an `Audit` or `DryRun` policy is not written to the audit log |
| Policies | `AuditSink` is decoded and never used. So are the `DataSource` fields `schemas`, `tables`, `healthCheck`, `attachMode`, `readonly` and `prefix`, `SubjectBinding.groupsSource`, and `RelationshipPolicy.backend.type` and `cacheTtl` |
| Reject rules | The `query.*` values describe the outer SELECT only |
| Approvals | `columnsAccessed` does not trigger on `SELECT *`. `predicates` miss `IN` and `lower()`. An expired grant answers 202 again |
| ReBAC | A backend error returns 500 `ERR_INTERNAL` |
| OPA | `POST /admin/reload` answers `ok` when a Rego module is broken. A `.rego` change does not trigger a reload |
| Statements | With an allow policy that names no table, `SHOW` lists tables that a deny policy covers |
| Statements | On a query without filter, mask or limit, some forms that DuckDB reads in another way return 500 |
| Identity | The JWT `iat` claim is not checked. JWT mode reads only subject id, email and groups |
| Config | A `SLUICE_*` variable is read only when the key has a default or is in the file. `SLUICE_ADMIN__TOKEN` alone leaves the admin API without a token |
| Config | `admin.token` does not resolve `secret://`. A secret file with a final newline is not trimmed for the pepper and the genesis |
| Config | `sluice config validate` accepts an unknown `policies.engine`. `--strict` does not check the server config. `datasources.directory` is never read |
| Metrics | `sluice_policy_*` and `sluice_schema_*` are never registered. `sluice_audit_rotate_duration_seconds` is never observed |
| Audit | No record for a 401, a transport 429 or a full concurrency slot. The `query-result` record has an empty subject |
| CLI | `sluice policy test` exits 1 on a policy compile error, the help says 3. `sluice schema export` without `--kind` exits 1. `sluice budget show` deletes old rows |
| MCP | `list_tables` and `list_accessible_tables` are empty until `describe_table` fills the schema cache. `sluice mcp` over stdio cannot finish an approval |
| Data sources | A removed SQLite file returns `ERR_INTERNAL`, not `ERR_DATASOURCE_UNAVAILABLE` |
| REST | `GET /openapi.json` is public and names paths without describing an operation |
| MCP over HTTP | The listener on `mcp.listen` has no TLS setting and no rate limit before login |
| Audit | The hash chain has no secret key. `sluice audit verify` accepts a log whose later hashes were computed again. The help text says a line that is not JSON exits 1, the command exits 4 |
| Limits | A row limit and `rowsPerDay` count result rows. One aggregate such as `list()` returns a whole table in one row |
| Reject rules | In a `UNION` the `query.*` values describe only the first SELECT. `query.joins` does not count a comma join |
| Masks | `sluice policy validate` accepts a `saltRef` or `keyRef` that is not a secret reference. Queries on the table then return 500 |
| Approvals | A link preview that sends a plain `GET` decides the approval. Only `HEAD` and prefetch headers are ignored |
| Statements | `SET` reaches DuckDB for a subject with an allow policy without `resources` |
| Docker examples | The container runs as uid 65532 and may not be able to write to the `./data` mount. Not checked, Docker was not available |

One more finding was reported to the maintainer directly and is not described here.

## Independent review

The review ran six times. Each round after the first checked the fixes of the round before. It ended when a round found no serious problem.

Fresh agents that wrote no page checked the work against the task rules and the code. They ran the examples, sent their own queries to scratch servers and compared each page with its old version.

| Round | Scope | Serious | Minor | Result |
|---|---|---:|---:|---|
| 1 | All pages, examples, scripts and this report | 26 | 78 | All serious findings were real and are fixed. 76 minor findings are fixed, 2 are open points below |
| 2 | The fixes of round 1 | 7 | 30 | 96 of 104 findings of round 1 were fully fixed, 8 partly. All 7 serious findings are fixed: 4 page facts, 1 example whose shown file was not the loaded file, 2 holes in the style check. The minor findings are fixed except the checker limits listed below |
| 3 | The fixes of round 2 | 4 | 11 | All 4 serious findings are fixed: the `SET` sentence was too broad for policies that name only the table, one threat row lacked its condition, one named `limits.maxRows` instead of `limits.maxRowsCeiling`, and the style check still accepted some files that nothing runs. The minor findings are fixed except the checker limits below |
| 4 | The fixes of round 3 | 3 | 5 | All 3 serious findings are fixed: the `SET` limit also holds for other subjects after one accepted `SET`, one table row was wrong for `query.group_by_columns`, and the style check accepted a suite below `policies.d/tests/` and a file of the wrong kind in a named directory. The 5 minor findings are fixed |
| 5 | The fixes of round 4 | 1 | 6 | The serious finding is fixed: a policy that names the catalog with a wildcard still matches a table name without catalog, so the `SET` limit and its advice now say "without a wildcard". 5 of the 6 minor findings are fixed. The key check for policy and config files has the limits named under "Examples" |
| 6 | The fixes of round 5 | 0 | 9 | No serious finding. The minor findings are fixed: one table row on `query.where_columns`, the words "without a wildcard" on two more pages, and gaps of the key checks. What stays open is listed under "Examples" and "Open points" |

Most serious findings of round 1 were limits or security claims that were stated too broadly, and refused SQL forms that the first rewrite had cut. Each such fix is pinned by a test case or a step.

## Open points

- The style check is a set of text rules. It misses a count when the noun is not in its list or a word stands between, a long sentence with an end mark inside, word forms of banned words, and rare forms of indented code. It flags a few fine sentences, for example one with the word "planned". A reviewer still has to read a new page.

- CI: the job `docs-check` passes on pull request 11. The lint job fails there on `internal/queryservice/execute_test.go:810` (staticcheck QF1001). It fails on `main` in the same way, and this branch does not change that file.
- The Docker paths in the docs and the compose files were not run.
- The Quickstart shows `git clone`, `make build` and the start script from files under `examples/install/manual/`. The maintainer agreed to `manual/` for docker, cosign, systemd and client config. This use is wider and needs a decision.
- No `CLAUDE.md` or `AGENTS.md` exists in the repository, so nothing links to `docs-style.md` from such a file. `examples/README.md` and the Makefile name it.
