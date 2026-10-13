# Audit: Architecture

Audited at commit 6f7b99f (binary `/tmp/sluice-bin/sluice`). Pages: `docs/architecture/index.md`, `request-lifecycle.md`, `policy-engines.md`, `security-model.md`.

How claims were checked:
- "run" means a real request against a scratch server (hello-sluice copy with SQLite, ports 18120 to 18130). Four setups: engine `yaml`, engine `yaml` with cache and a JWT binding, engine `composite` with `yaml, opa, rebac` and a fake OpenFGA, engine `opa` alone.
- No "run" means the code was read only.
- Go tests of `internal/policy`, `opaengine`, `rebac`, `queryservice`, `policycache`, `config`, `identity` pass (`GOFLAGS=-mod=readonly go test`).

## Summary

### Not implemented

- index.md:92, request-lifecycle.md:130, security-model.md:12: `PRAGMA` as an allowed statement. `StmtPragma` is on the allowlist (`internal/parser/shape.go:31`) but the pg_query backend never returns it (`internal/pgquery/stmt.go:14-62`). Run: `PRAGMA version` and `PRAGMA table_info(...)` give `ERR_SYNTAX`, also when policy allows.
- security-model.md:60: validation of the JWT `iat` claim. The parser that verifies tokens has no `WithIssuedAt()` (`internal/identity/jwt.go:195-200`). The parser that has it (`jwt.go:109-113`) is never used. Run: a token with `iat` one hour in the future is accepted.
- request-lifecycle.md:101: shadow entries of `Audit` and `DryRun` policies in the audit record. Only `dec.Applied` is copied (`internal/queryservice/audit_helpers.go:97`). No non-test code in queryservice, audit or transport reads `Decision.Shadow`. Run: an `enforcementMode: Audit` deny policy shows in `/admin/subjects/explain` (`shadow`) but not in the audit record.
- security-model.md:95: log statements that wrap secrets in `telemetry.Redacted`. The type exists (`internal/telemetry/slog.go:100`) but has no call site outside tests.

All three engines are wired and reachable from config: `yaml`, `opa`, `composite` with members `yaml`, `opa`, `rebac` (`cmd/sluice/policyengine.go:23-84`). Run for each.

### Docs and code disagree

- index.md:59. Doc: 24 error codes. Code: 23 (`pkg/errors/codes.go:15-52`).
- index.md:41, index.md:74, component map. Doc: six datasource types, "S3 Parquet". Code: seven types, `postgres`, `mysql`, `sqlite`, `s3`, `http`, `duckdb_file`, `motherduck` (`pkg/apitypes/datasource.go:58-64`). `s3_parquet` is refused (`pkg/apitypes/decoder.go:471-475`). `s3` and `http` read parquet, csv, json, xlsx.
- index.md:93, security-model.md:70. Doc: every catalog is attached `READ_ONLY`. Code: true for postgres, mysql, sqlite, duckdb_file, motherduck. `s3` and `http` are `ATTACH ':memory:'` plus views (`internal/datasource/drivers/s3/driver.go:212-224`, `httpfile/driver.go:189-201`).
- index.md:8. Doc: an audit record for every request. Code: none for a 401 (run), a transport 429 (`internal/transport/rest/ratelimit.go:27-30`) or a saturated concurrency slot (`internal/queryservice/execute.go:51-53`).
- index.md:92, security-model.md:12. Doc: the rewriter and the executor hold the statement allowlist. Code: only the rewriter (`internal/rewriter/rewriter.go:353-369`). The executor runs what it gets.
- request-lifecycle.md:131, security-model.md:14. Doc: `ATTACH` returns `ERR_UNSUPPORTED_SYNTAX`. Code: pg_query cannot parse `ATTACH 'x.db' AS y`, `LOAD httpfs`, `INSTALL httpfs`, run gives `ERR_SYNTAX`. DDL and `COPY` give `ERR_UNSUPPORTED_SYNTAX` (run). All of these codes, and `ACL_REJECTED` for writes, only show when policy allows the statement. Else the answer is `ACL_DENIED` (run).
- request-lifecycle.md:93. Doc: `cache.rewrite.enabled: true` memoises. Code: only with `policies.engine: yaml`. The cache needs `SnapshotInfo()` (`internal/queryservice/cache.go:18-21`) and only the YAML engine has it (`internal/policy/engine.go:61`). Run: with `composite` and the cache on, the same query is evaluated again (new OpenFGA check after the 10 s ReBAC cache).
- request-lifecycle.md:179, index.md:90, security-model.md:38. Doc: the access record is "durably enqueued". Code: it is put on an in-memory queue of 10000 records (`internal/audit/dispatcher.go:203-210`). The sink writes later.
- request-lifecycle.md:184. Doc: budget counts "execution CPU time". Code: wall-clock time from request start until the executor returns (`internal/queryservice/execute.go:421`, `internal/budget/manager.go:157`).
- request-lifecycle.md:199. Doc: Execute stage codes are `ERR_TIMEOUT`, `ERR_CANCELED`, `ERR_QUERY_FAILED`, `ERR_INTERNAL`. Code: also `ERR_DATASOURCE_UNAVAILABLE` (`execute.go:584-585`).
- request-lifecycle.md:196-198. Stage table misses codes: Policy can end with `ERR_INTERNAL` (engine error, `execute.go:203-209`, run for OPA and ReBAC). Approval can end with `ERR_INTERNAL` (no broker, `execute.go:252-260`). Rewrite can end with `ERR_INTERNAL` for a template variable the subject does not have (run).
- policy-engines.md:31. Doc: every engine gives the same decision shape. Code: OPA output has no rewrite effects and no approval (`internal/opaengine/contract.go:78-85`). ReBAC gives deny or abstain only (`internal/rebac/engine.go:127-184`).
- policy-engines.md:41. Doc: `sluice policy test` asserts exactly this contract and describes any engine. Code: it runs the YAML engine only (`internal/policytest/policytest.go:106`) and has no assertion for approval, row cap, sample or timeout (`policytest.go:69-78`).
- policy-engines.md:107. Doc: the same snapshot, subject and query always give the same decision. Code: true for the YAML engine. A ReBAC decision depends on the OpenFGA store.
- policy-engines.md:111. `sluice policy explain` uses the YAML engine only (`cmd/sluice/policy.go:144`). It does not show OPA or ReBAC results. `GET /admin/subjects/explain` uses the configured engine (`internal/queryservice/tables.go:52`).
- security-model.md:41. Doc: a template that references a variable the subject does not have denies. Code: `ERR_INTERNAL`, HTTP 500, audit decision `error` (run, `internal/rewriter/errors.go:55-78` has no case for `policy.ErrTemplateVarMissing`). A CEL condition error does deny with `ACL_DENIED` (`internal/policy/engine.go:112-126`).
- security-model.md:46. Doc heading: ReBAC backend errors deny. Code: `ERR_INTERNAL`, HTTP 500 (run).
- security-model.md:67. Doc: `credentialsRef` and `tokenRef` are injected with `CREATE SECRET`. Code: the MotherDuck token is set with `SET motherduck_token = '...'` (`internal/datasource/drivers/motherduck/driver.go:108`).
- index.md:77. Doc: `internal/approval` is in-memory. Code: SQLite store with `approval.persist: true` (`cmd/sluice/approval.go:56-68`).

### Broken code blocks

- None. The one YAML block (policy-engines.md:17) validates and boots.

### Proposed pages for this section

1. `architecture/index.md`. What Sluice does in two sentences, the component map, five rules a user can rely on (default deny, audit before rows, read-only statements, bound parameters, hot reload) with the corrected facts, the three reload triggers. Drop the package tour.
2. `architecture/request-lifecycle.md`. The sequence diagram, the stages in order with one line each, the corrected error code table. Move the SQL form details (aliases, 63 bytes, parse check forms) to a short "Limits" list with one line per case, or link to `policies/row-filters.md`.
3. `architecture/policy-engines.md`. Engine table and config block, what each engine can return, composite merge rules, a "Limits" list: `rebac` only inside `composite`, rewrite cache only with `yaml`, a `.rego` edit needs a manual reload, `policy test` and `policy explain` are YAML only.
4. Remove `architecture/security-model.md`. 13 of 27 claim rows repeat `security/index.md`, `security/threat-model.md` or the two pages above. The other facts (JWT and API keys, datasource credentials, budget store) have an owner page elsewhere. Move the fail-closed list (with the corrected codes) to `security/index.md`, the JWT and API key facts to `policies/subjects.md` or `security/hardening.md`. Fix the link in `security/index.md:35` and `architecture/index.md:119`.

### Features the code has that the section does not document

- `limits.disableCrossCatalog: true` refuses a query over more than one catalog with `ACL_REJECTED`, before policy (`internal/queryservice/execute.go:131-140`).
- A two-part table name whose first part is an attached catalog is refused with `ACL_REJECTED`, "write catalog.schema.table" (`execute.go:168-175`, run).
- A statement nested too deep is refused with `ERR_UNSUPPORTED_SYNTAX` (`execute.go:539-540`, `internal/pgquery/depth.go:49`).
- `policies.opa.moduleDir` is required for `opa`. Without it the server does not start (`internal/opaengine/engine.go:73-75`, run).
- The file watcher only reacts to `.yaml` and `.yml` (`internal/config/watch.go:225-235`). Run: a `.rego` edit was not loaded until `POST /admin/reload`.
- `policies.reload: false` turns the file watcher off. SIGHUP and `POST /admin/reload` still work (`cmd/sluice/serve.go:54-78`).
- `policies.rebac.cacheSize`, default 10000 (`internal/config/server.go:322`).
- `SET` passes the statement gate, but DuckDB config is locked (`internal/executor/harden.go:82`). Run: `SET threads`, `SET TimeZone`, `RESET` give `ERR_QUERY_FAILED`. One `SET` case needs a maintainer decision and was reported outside this file. Do not document `SET` as a feature before that.

## docs/architecture/index.md

Prose words now: 701. Suggested after rewrite: 220.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-9 | Sluice parses each statement, decides, rewrites it and audits | implemented (`internal/queryservice/execute.go:36`) | run |
| 2 | 8-9 | Audit record for every request, allowed or denied | mismatch | No record for 401 (run), transport 429 (`internal/transport/rest/ratelimit.go:27-30`), saturated slot (`execute.go:51-53`) |
| 3 | 11-14 | One orchestrator, transports are thin, same decision on every transport | internal detail | `cmd/sluice/bootstrap.go:345-383`. Keep one line: REST and MCP give the same decision |
| 4 | 41 | Execution in embedded DuckDB | implemented (`internal/executor/executor.go:106-134`) | run |
| 5 | 41-42 | Six datasource types, attached as read-only catalogs | mismatch | Seven types (`pkg/apitypes/datasource.go:58-64`). `s3`, `http` are in-memory catalogs with views (`internal/datasource/drivers/s3/driver.go:212`) |
| 6 | 42-43 | Cross-source joins work | implemented (`cmd/sluice/bootstrap.go:166-180`) | Not run. Off with `limits.disableCrossCatalog` |
| 7 | 48-50 | License split `pkg/` Apache-2.0, `internal/` AGPL, `cmd/sluice` wires them | internal detail | SPDX headers, for example `pkg/errors/codes.go:1` |
| 8 | 56 | `pkg/apitypes` holds all 11 kinds | internal detail | Count is right (`pkg/apitypes/typemeta.go:21-31`) |
| 9 | 57-58 | `pkg/mask`, `pkg/datasource` rows | internal detail | |
| 10 | 59 | `pkg/errors` has the 24 canonical codes | mismatch | 23 (`pkg/errors/codes.go:15-52`) |
| 11 | 65-84 | Table of 20 `internal/` packages | internal detail | Wrong in it: "six source types" (seven), `internal/approval` "in-memory" (SQLite with `approval.persist`). `internal/version` is missing |
| 12 | 88-89 | Default deny: empty `policies.d/` is valid and denies all, no allow means deny, no snapshot denies | implemented (`internal/config/policies.go:66-67`, `internal/policy/conflict.go:145-153`, `internal/policy/engine.go:95-107`) | run. Duplicate of `security/index.md:8-10` |
| 13 | 90-91 | Query is not served unless the audit record is durably enqueued first | mismatch | Gate exists (`internal/queryservice/audit_helpers.go:212-225`), default on (`internal/config/server.go:334`). "Durably" is wrong: in-memory queue (`internal/audit/dispatcher.go:203-210`) |
| 14 | 92-93 | Rewriter and executor allow only SELECT, EXPLAIN, SET, SHOW, PRAGMA. Writes rejected. Datasources READ_ONLY | mismatch | Gate only in the rewriter (`internal/rewriter/rewriter.go:353-369`). PRAGMA never parses (see request-lifecycle row 30b). `s3`, `http` not READ_ONLY |
| 15 | 94-95 | Policies in YAML or Rego, hot reload without restart | implemented (`internal/config/watch.go:193-221`) | run. Rego edits need a manual reload |
| 16 | 96-97 | Row filter values and template references are bound `$N` parameters | implemented (`internal/rewriter/inject_filter.go:404-448`) | tested `examples/policies/tests/basic.yaml:14` |
| 17 | 101-102 | Loads `*.yaml` and `*.yml` recursively, skips dot directories, `testdata`, `tests`. Multi-document files | implemented (`internal/config/policies.go:167-198`, `pkg/apitypes/decoder.go:129`) | |
| 18 | 103-104 | Duplicate kind and name, or an invalid document, fails the load | implemented (`internal/config/policies.go:99-119`, `:138-140`) | |
| 19 | 105-106 | Compile step: selectors, CEL, templates, mask arguments | internal detail | `internal/policy/snapshot.go:133-165` |
| 20 | 107 | Atomic pointer swap, in-flight evaluations keep their view | internal detail | `internal/policy/engine.go:71-78` |
| 21 | 108 | A snapshot that does not compile is rejected, the old one stays | implemented (`internal/config/watch.go:205-211`, `cmd/sluice/bootstrap.go:402-405`) | Limit: the check compiles YAML only. Run: with a broken `.rego`, `POST /admin/reload` answers `ok:true`, OPA keeps the old modules, a WARN is logged |
| 22 | 110-111 | Reload by file watcher (250 ms debounce), SIGHUP, `POST /admin/reload` | implemented (`internal/config/watch.go:86-88`, `cmd/sluice/serve.go:58-78`, `internal/transport/admin/routes.go:21`) | run, all three. Watcher needs `policies.reload: true` (default) |
| 23 | 111-112 | Reload rebuilds API key bindings, rate limit and budget specs, purges the rewrite cache, invalidates the schema cache | internal detail | `cmd/sluice/bootstrap.go:445-500`. User fact: keys, rate limits, budgets follow a reload |
| 24 | 115-119 | "Read on" list | fluff | Navigation. Third link goes away with the merge |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 18 | mermaid | Component map | untested | Labels checked, see below |

Diagram labels the code proves wrong:
- `S4[(S3 Parquet)]`: the type is `s3` with formats parquet, csv, json, xlsx. `s3_parquet` is refused (`pkg/apitypes/decoder.go:471-479`).
- Edge `ADM --> ID[identity]`: the admin server does not use the identity layer. It checks a static bearer token (`internal/transport/admin/auth.go:16-31`) and gets no `Identifier` (`cmd/sluice/bootstrap.go:426-435`).
- Not a wrong label, but missing: the `http` datasource type (`pkg/apitypes/datasource.go:62`).
- Right: `REST (:8080)` (`internal/config/server.go:292`), `Admin (:9091)` (`server.go:303`, off by default), `MCP (stdio / streamable HTTP)` (`internal/transport/mcp/server.go:26-27`).

## docs/architecture/request-lifecycle.md

Prose words now: 1689. Suggested after rewrite: 600.

Stage order in the code (`internal/queryservice/execute.go`): slot (51), rate limit (57), budget (73), SQL size and clamps (85-105), parse (110), cross-catalog gate (131), function gate (146), `$n` (155), ambiguous name (168), cache or policy (182-210), deny or reject (216-231), parse error (236), approval (251), rewrite (282), policy timeout and row cap (308), execute (336), post-query masks (359), audit gate (391). The doc order matches. The doc leaves out lines 131 and 168.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-6 | REST and MCP run the same pipeline. Admin explain uses the same policy evaluation and does not execute | implemented (`internal/queryservice/execute.go:29`, `internal/queryservice/tables.go:30-75`) | run for REST and admin |
| 2 | 41-43 | `limits.globalRps` and `limits.perIpRps` give 429 `ERR_RATE_LIMITED` before identity | implemented (`internal/transport/rest/routes.go:35`, `internal/transport/rest/ratelimit.go:43-64`) | REST only (`cmd/sluice/bootstrap.go:354-362`). Not run |
| 3 | 44-45 | `limits.maxConcurrent`, `ERR_RATE_LIMITED` after a short wait | implemented (`execute.go:437-457`) | Wait is 50 ms. Not run |
| 4 | 46-48 | Per-subject token bucket, falls back to issuer, then `limits.defaultSubjectRps` | implemented (`execute.go:57-69`, `internal/ratelimit/limiter.go:105-141`) | Not run |
| 5 | 49-50 | Daily budget check before work, `ERR_BUDGET_EXCEEDED` | implemented (`execute.go:73-83`, `internal/budget/manager.go:125-144`) | Needs `budget.enabled`. Not run |
| 6 | 51-53 | SQL over `limits.maxSqlBytes` gives `ERR_PAYLOAD_TOO_LARGE`. Timeout and row count are clamped | implemented (`execute.go:85-105`) | run, HTTP 413 |
| 7 | 57 | Parsed with pg_query | implemented (`internal/pgquery/parser.go:52-107`) | |
| 8 | 57-59 | Unquoted identifier with a non-letter Unicode character is a parse error | implemented (`internal/pgquery/parser.go:115-137`, `execute.go:543-544`) | run, `ERR_SYNTAX` |
| 9 | 59-60 | Regex fallback still extracts table names | internal detail | `execute.go:116-118`, `internal/parser/regex.go:36` |
| 10 | 60-63 | A parse error shows only when policy would allow. Deny and reject win | implemented (`execute.go:216-245`) | run. tested `TestExecute_ParseErrorAuditCodeMatchesClient` |
| 11 | 65-68 | Identifier over 63 bytes gives `ERR_UNSUPPORTED_SYNTAX` | implemented (`internal/pgquery/parser.go:140-172`, `execute.go:541-542`) | run. Reason text is internal detail |
| 12 | 70-74 | `FROM` alias that is a DuckDB keyword gives `ERR_SYNTAX` | implemented (`internal/parser/keywords.go:13-24`, `internal/pgquery/aliases.go:20`, `execute.go:545-546`) | run with `semi` and `qualify(x)` |
| 13 | 78-83 | Function gate before cache and policy. Listed forms give `ACL_REJECTED` | implemented (`execute.go:142-152`, `internal/parser/functions.go:16-49`, `internal/pgquery/functions.go:114`, `:142-146`) | run, 8 forms. Duplicate of `security/threat-model.md:65` |
| 14 | 83 | Audited with decision `reject`, no applied policies | implemented (`execute.go:147-149`) | run |
| 15 | 84-86 | `$n` gives `ERR_UNSUPPORTED_SYNTAX`, audited as `error` | implemented (`execute.go:155-162`) | run |
| 16 | 90-91 | Engine gives outcome, filters, masks, rewrite effects, rejections, approval | internal detail | `internal/policy/decision.go:25-50` |
| 17 | 93 | `cache.rewrite.enabled: true` memoises decision and rewrite in an LRU | mismatch | Only with engine `yaml` (`internal/queryservice/cache.go:18-21`, `internal/policy/engine.go:61`). run for `composite`. Default off, size 4096, TTL 60 s (`internal/config/server.go:349-353`) |
| 18 | 94-98 | Cache key parts, schema cache not in the key, entry lives until TTL or reload | internal detail | `internal/policycache/cache.go:26-31`. Keep one "Limits" line: a cached rewrite keeps the old column list of a masked table until `cache.rewrite.ttl` or a reload |
| 19 | 98-99 | Only cleanly parsed queries are cached. Rate limits, budgets, approvals, audit run on every request | implemented (`internal/queryservice/cache.go:15`, `execute.go:182-194`) | tested `TestExecute_CacheHitSkipsEvaluateAndRewrite` |
| 20 | 100-101 | Only `Enforce` policies (the default) shape the decision. `Audit` and `DryRun` policies are kept as shadow entries in the decision | implemented (`internal/policy/conflict.go:34-48`) | run: shown as `shadow` by `/admin/subjects/explain` |
| 20b | 101-102 | Shadow entries are recorded in the audit record | not implemented | Only `dec.Applied` is copied (`internal/queryservice/audit_helpers.go:97`). run: no shadow field in the record |
| 21 | 103-105 | Deny gives `ACL_DENIED` with message and code of the top deny policy. Reject gives `ACL_REJECTED` or the rule code | implemented (`execute.go:216-231`, `:474-506`) | run |
| 22 | 109-110 | Single-use grant for subject and exact SQL hash | implemented (`execute.go:261-268`, `internal/approval/broker.go:330-349`) | Not run. tested `TestApproval_EndToEndWithRealBroker`. Duplicate of `policies/approvals.md` |
| 23 | 110-113 | Parked, slot released, webhooks, wait up to `approval.syncWait` (20 s), one re-run on approval | implemented (`execute.go:276-277`, `internal/queryservice/approval.go:39-90`, `internal/config/server.go:356`) | Not run |
| 24 | 113-116 | `ERR_APPROVAL_PENDING` (202), `ERR_APPROVAL_REJECTED`, `ERR_APPROVAL_EXPIRED`, full broker `ERR_RATE_LIMITED` | implemented (`internal/queryservice/approval.go:47-113`, `pkg/errors/codes.go:82-84`) | Not run |
| 25 | 120-125 | Each filtered or masked table becomes a subquery with `sluice_src`, bound parameters, columns from the schema cache | internal detail | `internal/rewriter/inject_filter.go:25-31` |
| 26 | 125-126 | Masked table missing in the schema cache gives `ERR_REWRITE_FAILED`. Failed schema load gives `ERR_DATASOURCE_UNAVAILABLE` | implemented (`internal/rewriter/errors.go:72-75`) | Not run |
| 27 | 126-128 | Outer `SELECT *` is expanded when a post-query mask applies | internal detail | `internal/rewriter/expand_star.go:27` |
| 28 | 128 | `LIMIT` injected when absent, constant `LIMIT` above the cap clamped | implemented (`internal/rewriter/apply_rewrite.go:18-50`) | Only with a `QueryRewritePolicy` limit. Not run. tested `internal/policy/rewritepolicy_test.go` |
| 29 | 128-130 | Sample wraps the statement in `SELECT * FROM (...) AS sluice_sample USING SAMPLE` | internal detail | `internal/rewriter/apply_rewrite.go:55-60` |
| 30 | 130-132 | Only SELECT, EXPLAIN, SET, SHOW are allowed. Writes give `ACL_REJECTED`. DDL, COPY, ATTACH give `ERR_UNSUPPORTED_SYNTAX` | mismatch | Gate: `internal/parser/shape.go:29-36`, `internal/rewriter/rewriter.go:353-369`. run: writes `ACL_REJECTED`, DDL and COPY `ERR_UNSUPPORTED_SYNTAX`, ATTACH `ERR_SYNTAX`. Only when policy allows, else `ACL_DENIED`. `SHOW tables` works, `SET` see summary |
| 30b | 130 | PRAGMA is an allowed statement | not implemented | On the allowlist (`internal/parser/shape.go:31`), but pg_query cannot parse it and `internal/pgquery/stmt.go:14-62` never returns `StmtPragma`. run: `ERR_SYNTAX` |
| 31 | 132-133 | `QueryRewritePolicy` timeout and row cap only tighten | implemented (`execute.go:308-314`) | Not run |
| 32 | 135-136 | `EXPLAIN` refused when a filter or mask applies | implemented (`internal/rewriter/rewriter.go:180-183`) | run, also `EXPLAIN ANALYZE` |
| 33 | 136-138 | Unaliased filtered table with a keyword name is refused | implemented (`internal/rewriter/inject_filter.go:274-277`) | Not run |
| 34 | 138-140 | `sluice_src` is reserved | implemented (`internal/rewriter/inject_filter.go:49-63`) | run |
| 35 | 140-141 | Rewriter checks no table is read outside its subquery | internal detail | `internal/rewriter/inject_filter.go:72` |
| 36 | 143-150 | Normalization for DuckDB, then a parse check in a separate DuckDB instance. Differ gives `ERR_UNSUPPORTED_SYNTAX`, unparsable gives `ERR_SYNTAX` | implemented (`internal/rewriter/dialect.go:322-337`) | Not run. Instance sizing (one thread, two connections) is internal detail (`internal/executor/parsetree.go:29-40`). Duplicate of `policies/row-filters.md` |
| 37 | 152-154 | Check failure `ERR_INTERNAL`, too large `ERR_PAYLOAD_TOO_LARGE`, canceled `ERR_CANCELED`, timed out `ERR_TIMEOUT` | implemented (`internal/rewriter/dialect.go:347-353`, `internal/executor/parsetree.go:64-69`, `internal/rewriter/errors.go:68-71`) | Not run |
| 38 | 154-155 | Mask snippets get the check once per text. Pass-through queries are not checked | internal detail | `internal/rewriter/mask_snippet.go:60-67`, `internal/rewriter/rewriter.go:187-196` |
| 39 | 155-157 | Column qualified with schema or catalog of a filtered table is refused | implemented (`internal/rewriter/inject_filter.go:121`) | run |
| 40 | 161-163 | Streams rows. Timeout by context gives `ERR_TIMEOUT`. Client disconnect gives `ERR_CANCELED` | implemented (`internal/executor/executor.go:106-134`, `execute.go:575-590`) | Not run |
| 41 | 167-168 | `hash` with `hmac_sha256`, `fpe`, `jitter`, `fake` run in Go on result rows | implemented (`pkg/mask/hash.go:67-69`, `pkg/mask/fpe.go:64`, `pkg/mask/jitter.go:40`, `pkg/mask/fake.go:59`) | Reason text is internal detail. Owner: `policies/column-masks.md` |
| 42 | 169-173 | Raw pass-through in the subquery, masks built before the audit gate | internal detail | `execute.go:356-384` |
| 43 | 173-175 | Result column count and names are checked, mismatch gives `ERR_MASK_UNSUPPORTED_CONTEXT` | implemented (`internal/queryservice/maskedrows.go:89-105`) | Not run |
| 44 | 179-182 | Access record must be durably enqueued before the first row, else `ERR_AUDIT_UNAVAILABLE`. `audit.failClosed: false` serves anyway | mismatch | Gate and flag are right (`execute.go:386-395`, `audit_helpers.go:212-225`). "Durably" is wrong, in-memory queue, deadline 200 ms (`internal/audit/dispatcher.go:78`, `:203-229`). Duplicate of `security/audit.md` |
| 45 | 182-184 | Close writes a completion record with the row count and records budget usage as execution CPU time | mismatch | Record and budget write are right (`audit_helpers.go:289-324`). Time is wall-clock from request start to executor return (`execute.go:421`) |
| 46 | 190-193 | Table: slot, rate limit, budget, input caps | implemented | See rows 3 to 6 |
| 47 | 194 | Table: Parse codes | implemented (`execute.go:528-551`) | |
| 48 | 195 | Table: Function gate codes | implemented (`execute.go:146-162`) | Missing gates: cross-catalog and ambiguous name, both `ACL_REJECTED` |
| 49 | 196 | Table: Policy codes | mismatch | Also `ERR_INTERNAL` on an engine error (`execute.go:203-209`), run for OPA and ReBAC |
| 50 | 197 | Table: Approval codes | mismatch | Also `ERR_INTERNAL` when no broker is configured (`execute.go:252-260`) |
| 51 | 198 | Table: Rewrite codes | mismatch | List is right, but ATTACH is not there (see row 30) and `ERR_INTERNAL` also comes from a missing template variable (run) |
| 52 | 199 | Table: Execute codes | mismatch | `ERR_DATASOURCE_UNAVAILABLE` is missing (`execute.go:584-585`) |
| 53 | 200-201 | Table: post-query masks and audit gate codes | implemented (`internal/queryservice/maskedrows.go:90`, `execute.go:367-373`, `audit_helpers.go:221`) | |
| 54 | 203 | Link to error codes page | fluff | Navigation |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 10 | mermaid | Sequence diagram of the pipeline | untested | No label is wrong. Order matches `execute.go`. Not shown: the function gate between parse and evaluate |

## docs/architecture/policy-engines.md

Prose words now: 761. Suggested after rewrite: 450.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-12 | `policies.engine`: `yaml` (default), `opa`, `composite` | implemented (`cmd/sluice/policyengine.go:23-47`, `internal/config/server.go:319`) | run, all three. `sluice config validate` does not check the value (run) |
| 2 | 14 | `policies.composite.members` defaults to `["yaml"]`, may hold `opa` and `rebac` | implemented (`internal/config/server.go:320`, `cmd/sluice/policyengine.go:53-84`) | run |
| 3 | 15 | `rebac` is not a standalone engine value | implemented (`cmd/sluice/policyengine.go:45-46`) | run: `unknown policies.engine "rebac"` |
| 4 | 31-39 | Every engine gives the same decision shape (outcome, filters, masks, rewrite effects, rejections, approval, applied names) | mismatch | Go type is shared (`internal/policy/decision.go:25-50`). OPA cannot return rewrite effects or approval (`internal/opaengine/contract.go:78-85`). ReBAC returns deny or abstain only |
| 5 | 41-43 | `sluice policy test` asserts exactly this contract, a YAML suite describes any engine | mismatch | YAML engine only (`internal/policytest/policytest.go:106`). No assertion for approval, row cap, sample, timeout (`policytest.go:69-78`) |
| 6 | 47-48 | YAML engine compiles into an immutable snapshot | internal detail | `internal/policy/engine.go:71-78` |
| 7 | 50 | Order: priority descending, then name ascending | implemented (`internal/policy/snapshot.go:157-162`) | |
| 8 | 51-52 | Deny overrides, top deny gives message and code | implemented (`internal/policy/conflict.go:118-144`) | |
| 9 | 53-54 | No matching allow means deny, recorded as abstain | implemented (`internal/policy/conflict.go:145-153`) | run. tested `TestEngine_DefaultDenyIsAbstained` |
| 10 | 55-58 | Every table needs an allow, else `no SqlAccessPolicy allows <table>`, abstain | implemented (`internal/policy/conflict.go:155-169`) | tested `internal/policy/per_table_allow_test.go` |
| 11 | 59-60 | Row filters fold by `combine`: `restrictive` AND, `permissive` OR | implemented (`internal/policy/conflict.go:246-264`) | Owner: `policies/row-filters.md` |
| 12 | 61-62 | Mask per column: priority, then selector specificity, then name | implemented (`internal/policy/conflict.go:295-333`) | Owner: `policies/column-masks.md` |
| 13 | 63 | Rewrites: minimum row cap, minimum timeout, first sample | implemented (`internal/policy/conflict.go:340-359`) | |
| 14 | 64-65 | A condition that errors at runtime denies the request | implemented (`internal/policy/engine.go:112-126`) | Not run |
| 15 | 69 | OPA runs in-process | implemented (`internal/opaengine/engine.go:87`) | run |
| 16 | 69-70 | Top-level `*.rego` under `policies.opa.moduleDir`, compiled at startup and on every policy reload | implemented (`internal/opaengine/engine.go:72-93`, `:146-164`, `cmd/sluice/bootstrap.go:211`, `:452`) | run. Limit: a `.rego` edit alone starts no reload (`internal/config/watch.go:225-235`, run) |
| 17 | 70-71 | Decision read from `policies.opa.query`, default `data.sluice.main` | implemented (`internal/config/server.go:321`) | run |
| 18 | 71-73 | Input: subject, action, tables, shape, request. Output: `allow`, `abstain`, `deny_reason`, `row_filters`, `column_masks`, `rejections` | implemented (`internal/opaengine/contract.go:18-58`, `:78-85`) | run for `allow`, `row_filters`, `rejections`. Duplicate of `policies/opa.md` |
| 19 | 74 | Unknown output fields fail closed | implemented (`internal/opaengine/contract.go:115-120`) | run: `ERR_INTERNAL`, HTTP 500 |
| 20 | 74-75 | Predicates and masks go through the YAML compile paths | internal detail | `internal/opaengine/contract.go:151`, `:166` |
| 21 | 80-83 | `rebac` checks `RelationshipPolicy` against OpenFGA, renders `objectTemplate` and `subjectTemplate`, posts to the check endpoint | implemented (`internal/rebac/engine.go:188-213`, `internal/rebac/client.go:68-99`) | run with a fake OpenFGA: `POST /stores/<id>/check` |
| 22 | 83-85 | All true: abstain. Any false: deny. Backend error: error. No matching policy: abstain | implemented (`internal/rebac/engine.go:127-184`) | run: false gives `ACL_DENIED`, backend down gives `ERR_INTERNAL` |
| 23 | 85-86 | LRU cache, `policies.rebac.cacheTtl` default 10 s, errors not cached | implemented (`internal/config/server.go:322`, `internal/rebac/engine.go:197-207`) | run |
| 24 | 91-100 | Composite merge: member error fails, first non-abstained deny wins, rejections union, one allow needed, filters AND, masks first member wins, rewrites restrictive, approvals add up | implemented (`internal/policy/composite.go:65-149`) | tested `internal/policy/composite_test.go`. run for deny, reject, allow |
| 25 | 99 | Losing masks are recorded as shadow entries | internal detail | In the decision only (`composite.go:129-131`), not in the audit record |
| 26 | 102-103 | Summary sentence of the merge rules | fluff | Repeats the list |
| 27 | 107-109 | Same snapshot, subject, query always give the same decision. No clock variable | mismatch | True for YAML and CEL (`internal/policy/cel.go:18`, `:33-35`). Not for ReBAC |
| 28 | 111 | `sluice policy explain --user U --table cat.sch.tbl` | implemented (`cmd/sluice/policy.go:89-237`) | run. YAML engine only (`policy.go:144`). Flag `--policies-dir` defaults to `./policies.d` |
| 29 | 112 | `GET /admin/subjects/explain` | implemented (`internal/transport/admin/routes.go:20`, `internal/transport/admin/subjects.go:18`) | run |
| 30 | 113 | `explain_access` MCP tool | implemented (`internal/transport/mcp/tools.go:60`) | Not run |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 17 | yaml | Server config with `engine: composite`, members `yaml, opa, rebac`, `opa.moduleDir`, `opa.query` | untested, verified by hand | No test builds this config (`buildPolicyEngine` has no test). `sluice config validate --config` says OK. `sluice serve` boots with the same keys (absolute paths) and all three members answer |

## docs/architecture/security-model.md

Prose words now: 912. Suggested after rewrite: 0 (merge into `security/index.md`, about 150 words move).

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-6 | Built fail-closed, page lists the guarantees | fluff | |
| 2 | 10-11 | Empty `policies.d/` denies all. No allow or no snapshot means `ACL_DENIED` | duplicate | Owner `security/index.md:8-10`. True (`internal/policy/engine.go:95-107`), run |
| 3 | 12-14 | Only SELECT, EXPLAIN, SET, SHOW, PRAGMA. Writes `ACL_REJECTED` "write operations are not permitted". DDL, COPY, ATTACH `ERR_UNSUPPORTED_SYNTAX` | mismatch | Same as request-lifecycle rows 30 and 30b. Message text is right (`internal/rewriter/rewriter.go:359-360`, run). Also in `security/index.md:11-13`, `security/threat-model.md:63-64` |
| 4 | 15-18 | `EXPLAIN` in any form refused on protected tables | duplicate | Owner `security/index.md:13-15`. True (`internal/rewriter/rewriter.go:180-183`), run |
| 5 | 19-27 | DuckDB parse check and its codes | duplicate | Owner `request-lifecycle.md:143-154`, also `security/threat-model.md:71`. True (`internal/rewriter/dialect.go:322-337`) |
| 6 | 28-36 | Function allowlist, `FROM` only `generate_series`, `range`, `unnest`, refused forms give `ACL_REJECTED` and name the function | duplicate | Owner `security/threat-model.md:65` and `request-lifecycle.md:78-83`. True (`internal/parser/functions_allow.go:23`, `internal/parser/functions.go:16-49`), run. The file path is internal detail |
| 7 | 37-40 | Fail-closed audit, `audit.failClosed: false` is best effort | duplicate | Owner `security/audit.md`, also `security/index.md:16-17`. True, but "durably" is wrong (see index row 13) |
| 8 | 41-43 | CEL condition error or a template variable the subject lacks denies the request | mismatch | CEL: deny (`internal/policy/engine.go:112-126`). Template: `ERR_INTERNAL`, HTTP 500 (run). No row is served in both cases |
| 9 | 44-45 | Rego decision with unknown fields, or no decision, fails closed | duplicate | Owner `policy-engines.md:74`. True: unknown field `ERR_INTERNAL` (run), no decision `ACL_DENIED` (`internal/opaengine/engine.go:107-110`) |
| 10 | 46-47 | OpenFGA error fails the request, errors are not cached | mismatch | Heading says deny. Code gives `ERR_INTERNAL`, HTTP 500 (run). Cache part is right (`internal/rebac/engine.go:203-207`). Owner `policy-engines.md:85-86` |
| 11 | 48-50 | Full approval queue gives `ERR_RATE_LIMITED`. Approval without a broker is refused | duplicate | Owner `policies/approvals.md`, also `request-lifecycle.md:115-116`. True (`internal/queryservice/approval.go:47-54`, `execute.go:252-260`). Start with an `ApprovalPolicy` and no `approval.publicBaseUrl` fails (`cmd/sluice/approval.go:39-41`) |
| 12 | 51-53 | `budget.failClosed: true` (default) aborts start when the store cannot open | implemented (`cmd/sluice/budgetwire.go:19-46`, `internal/config/server.go:368`) | Not run. Owner should be `operations/server-config.md` |
| 13 | 59 | JWT bearer or API key, resolved to a `SubjectBinding` | implemented (`internal/identity/jwt.go:157-236`, `internal/identity/apikey.go:133-158`) | run, both |
| 14 | 60 | JWT algorithms HS256/384, RS256/384, ES256/384 | implemented (`internal/identity/jwt.go:39`) | run: HS256, HS384 accepted, HS512 refused. RS and ES not run |
| 15 | 60 | `alg=none` is rejected | implemented (`internal/identity/jwt.go:166-168`) | run, 401 |
| 16 | 60-61 | `iss`, `aud`, `exp`, `nbf` validated with bounded clock skew | implemented (`internal/identity/jwt.go:171-230`) | run: unknown `iss`, wrong or missing `aud`, missing or old `exp`, future `nbf` give 401. Skew default 60 s (`jwt.go:188-194`). `aud` is checked only when the binding sets `audience` (`jwt.go:226-230`) |
| 16b | 60-61 | `iat` is validated | not implemented | Verifying parser has no `WithIssuedAt()` (`internal/identity/jwt.go:195-200`). The parser with it (`jwt.go:109-113`) is unused. run: `iat` one hour in the future is accepted |
| 17 | 61-63 | API key: constant-time HMAC-SHA256 compare with `hashRef`, timing jitter on failure | implemented (`internal/identity/apikey.go:154-157`, `:251-258`) | run for accept and reject. Jitter is 1 to 5 ms |
| 18 | 67-70 | Credentials are `secret://` refs (`credentialsRef`, `tokenRef`, schemes `env`, `file`), injected with `CREATE SECRET` | mismatch | postgres, mysql, s3, http use `CREATE SECRET` (`internal/datasource/drivers/postgres/driver.go:148-161`). MotherDuck `tokenRef` uses `SET motherduck_token` (`internal/datasource/drivers/motherduck/driver.go:108`). Owner `operations/data-sources.md` |
| 19 | 70-72 | Every catalog is attached `READ_ONLY` | mismatch | Not `s3`, `http` (see index row 5) |
| 20 | 74-76 | `secret://vault`, `aws-sm`, `gcp-sm` are rejected at config validation | implemented (`internal/secrets/urls.go:28-30`, `:56-59`, `internal/config/validate.go:109-114`) | run: `sluice config validate` exit 3. Keep as one "not implemented" line |
| 21 | 80-82 | Admin on its own address (default `:9091`, off by default), static bearer token, constant-time compare, empty token only warns, metrics on this listener behind the token | duplicate | Owner `security/index.md:18-20`. True (`internal/config/server.go:301-304`, `internal/transport/admin/auth.go:16-31`, `internal/transport/admin/server.go:108-109`, `internal/transport/admin/routes.go:26`), run |
| 22 | 86-91 | Approval URLs carry a 32-byte base64url token, constant-time compare, same 404 for unknown id and bad token, prefetch never changes state, single-use grant | duplicate | Owner `policies/approvals.md`, also `security/threat-model.md:76`. True (`internal/approval/broker.go:260`, `:501-505`, `internal/transport/rest/approvals.go:23-44`, `:102-112`). Not run |
| 23 | 95 | Secret values never show in logs, panics, metrics, audit records | duplicate | Owner `security/index.md:21-22`. Absolute claim, not checked line by line |
| 24 | 96 | Log statements wrap sensitive values in `telemetry.Redacted` | not implemented | Helper exists (`internal/telemetry/slog.go:100`), no call site outside tests |
| 25 | 97 | Audit records hold SQL fingerprints and a bounded SQL sample | implemented (`internal/audit/record.go:68-70`, `internal/queryservice/audit_helpers.go:25-30`) | run. Bound is `audit.sqlSampleBytes`, default 2048 |
| 26 | 101-105 | Non-goals: compromised host, malicious admin, aggregate inference | duplicate | Owner `security/threat-model.md:85-111` |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| none | | The page has no fenced block. Line 74 is a warning box | | |

Should this page stay: no. 13 of the rows repeat `security/index.md`, `security/threat-model.md`, `request-lifecycle.md` or `policy-engines.md` (11 classed `duplicate`, plus rows 3 and 10). The rest belongs to pages that exist: rows 13 to 17 to `policies/subjects.md`, rows 18 to 20 to `operations/data-sources.md`, row 12 to `operations/server-config.md`, rows 8, 24, 25 to `security/index.md`. Nothing is left that needs its own page.
