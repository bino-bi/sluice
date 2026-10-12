# Audit: Reference

Code at commit 6f7b99f (binary `/tmp/sluice-bin/sluice`, same code as HEAD). "(run)" in a row means I ran it against that binary. All other rows are from reading code.
Paths are relative to the repo root. `rest/`, `admin/`, `mcp/` mean `internal/transport/<name>/`.

## Summary

`make docs-check`: exit 0. All four generated pages equal the generator output. No CI workflow runs it (see below).

### Not implemented

- index.md, policy-schema.md, metrics.md, configuration.md: "drift gate in `docs-deploy.yaml` fails" and "`make docs-check` fails in CI". No workflow calls `make docs-check` or `scripts/check-generated-docs.sh`. `.github/workflows/docs-deploy.yaml:48-52` runs `make docs-generate` on push to main only.
- metrics.md: `sluice_policy_evaluations_total`, `sluice_policy_denials_total`, `sluice_policy_rejections_total`, `sluice_policy_eval_duration_seconds`. `policy.RegisterMetrics()` (internal/policy/metrics.go:31) has no caller. Absent from live `/metrics` after queries (run).
- metrics.md: `sluice_schema_cache_hits_total`, `sluice_schema_cache_misses_total`, `sluice_schema_refresh_duration_seconds`, `sluice_schema_stale_entries`. `schema.RegisterMetrics()` (internal/schema/metrics.go:107) has no caller. Absent from live `/metrics` (run).
- metrics.md: `sluice_audit_rotate_duration_seconds`. Registered (internal/audit/metrics.go:37) but nothing observes it, so no series is ever exported.
- configuration.md: `datasources.directory`. No code reads it. A wrong path passes `config validate` (run). DataSources load from `policies.directory`.
- configuration.md: `datasources.reload`. Only read by `Validate`, which rejects `true` (internal/config/validate.go:36). (run)
- configuration.md: Vault, AWS SM, GCP SM secret providers. Rejected (internal/secrets/urls.go:57). (run)
- policy-schema.md: kind `AuditSink` (summary row and all 16 field rows). Manifests decode into `Snapshot.AuditSinks` (internal/config/policies.go:131) and nothing reads that. Sinks come from `audit.*` in server.yaml (cmd/sluice/auditwire.go:34-102). Types s3, syslog, postgres, otlp are refused at validation (pkg/apitypes/decoder.go:505). No Postgres sink exists.
- policy-schema.md: `DataSource.healthCheck`, `healthCheck.query`, `healthCheck.interval`. No code reads `Spec.HealthCheck` (internal/datasource/registry.go:219 builds the driver spec without it).
- policy-schema.md: `SubjectBinding.groupsSource`. No reader.
- policy-schema.md: `QueryRewritePolicy.rewrite.hint`. Compile refuses it (internal/policy/snapshot.go:447). (run)
- rest-api.md: Arrow output. The page already says so. Rejected at rest/query.go:98 and :113. (run)
- rest-api.md: generated OpenAPI spec ("planned"). Only a stub exists (rest/version.go:36).
- rest-api.md: `params`. The page already says so. `$n` is refused (internal/queryservice/execute.go:155). (run)

### Docs and code disagree

- admin-api.md:24-28. Doc: TLS on the admin listener is not implemented, `admin.tls` fails validation. Code: admin TLS and mTLS work (internal/config/validate.go:21, cmd/sluice/bootstrap.go:423, admin/server.go:136). `config validate` exits 0 and the admin port serves HTTPS (run). docs/security/hardening.md:37 already says it works.
- configuration.md:104. Doc: all `*Ref` / `genesis` / `token` fields take a `secret://` URI. Code: `admin.token` is a literal (cmd/sluice/bootstrap.go:421). With `token: "secret://env/X"` the server accepts the string `secret://env/X` as the bearer token and rejects the env value (run).
- configuration.md:18-20 and cli.md:15-17. Doc: every field has a `SLUICE_` env override. Code: env only works for keys with a viper default or present in the file (internal/config/server.go:386-390, 419-496). Ignored when not in the file (run): `SLUICE_MCP__LISTEN`, `SLUICE_ADMIN__TOKEN`, `SLUICE_IDENTITY__APIKEYPEPPER`, `SLUICE_AUDIT__FILE__PATH`. With `admin.token` set in the file, `SLUICE_ADMIN__TOKEN` does override it (run). No default exists either for `duckdb.tempDir`, `datasources.reload`. The listed `SLUICE_REST__TLS`, `SLUICE_ADMIN__TLS`, `SLUICE_AUDIT__FILE`, `SLUICE_AUDIT__SYSLOG`, `SLUICE_AUDIT__S3`, `SLUICE_APPROVAL__WEBHOOKS` name struct or list keys and cannot work.
- cli.md:193, policy-schema.md:8, configuration.md:116, index.md:23. Doc: `sluice schema export` prints a oneOf union of all 11 kinds. Code: exits 1 with "cycle detected for type apitypes.Predicate" (run). `--kind RowFilterPolicy` fails the same way. The other 10 kinds work. `--kind` output has no `$schema` key. cmd/sluice/schema.go:60-89. No test covers the command.
- policy-schema.md:6. Doc: policies are validated against a JSON Schema bundled in the binary. Code: YAML decodes into Go structs and Go functions validate (pkg/apitypes/decoder.go:162, :216). The schema is only built at run time by `schema export` (cmd/sluice/schema.go:65).
- policy-schema.md:16. Doc: every kind uses `apiVersion: sluice.bino.bi/v1alpha1`. Code: `RelationshipPolicy` and `DataClassification` need `sluice.bino.bi/v1beta1` (pkg/apitypes/decoder.go:39, :51-52). v1alpha1 gives "unknown kind" (run).
- policy-schema.md, column "Notes". Doc: `required` / `optional`. Code: the generator derives it from the yaml `omitempty` tag only (scripts/gen-policy-docs.go:218-225). Wrong at least for `DataSource.readonly` and `healthCheck.interval` (marked required, omitting passes, run) and for `path`, `connection`, `bucket`, `urls`, `database` (marked optional, required per type, pkg/apitypes/decoder.go:424-492).
- policy-schema.md:139,140,161. Doc: empty type cell for `filter.predicate.value`, `filter.predicate.values`, `mask.args.value`. Code: type is `any` (generator prints nothing for interface types, scripts/gen-policy-docs.go:252-259).
- metrics.md:7-8. Doc: metrics use `_total` / `_seconds` / `_bytes` suffixes. Code: no `sluice_*_bytes` metric exists.
- error-codes.md:7-8. Doc: every API response, CLI message and audit record carries one of these codes. Code: CLI prints plain text (run). A policy `errorCode` puts any string in `code` (internal/policy/conflict.go:134, internal/queryservice/execute.go:483). `MY_CUSTOM_CODE` came back with 403 (run). Approval endpoints return `{"error":"not found"}`. Router 404 and 405 are plain text.
- rest-api.md:135. Doc: every 4xx/5xx from query and meta endpoints is a JSON APIError. Code: `GET /v1/query` gives 405 `text/plain`, unknown paths 404 `text/plain` (run).
- rest-api.md:30. Doc: `sql` is required. Code: no check. `{}` returns 403 `ACL_DENIED` (run).
- admin-api.md:48. Doc: `/admin/datasources` returns a list. Code: `{"datasources":[...]}`; `last_error` and `latency_ms` are left out when empty or 0 (admin/datasources.go:10-22). (run)
- admin-api.md:67. Doc: `simulated_sql` is evaluated instead of the table. Code: `table` stays required and is added to the tables of the SQL (admin/subjects.go:29, internal/queryservice/tables.go:39-50). (run)
- admin-api.md:131. Doc: "configure the scrape with a credentials file". The YAML below it uses inline `credentials`.
- mcp.md:53-60, 81-85. Doc: `list_tables` and `list_accessible_tables` return the tables of a catalog / the tables the identity may query. Code: they list only tables already in the schema cache (internal/queryservice/tables.go:174). A fresh server returns `[]`; tables show up after a query or `describe_table` touched them (run). A policy reload empties the cache (cmd/sluice/bootstrap.go:499).
- mcp.md:106. Doc: only an explicit Deny hides a table. Code: every deny outcome hides it, default-deny included (internal/queryservice/tables.go:250-259). (run)
- mcp.md:42. Doc: `row_limit` range 1..100000. Code: no range check. 0 or less means the default, larger values are cut to `limits.maxRowsCeiling` (internal/queryservice/execute.go:100-105). (run)
- mcp.md:144-146. Doc: a runnable `sluice serve` variant ships in `examples/mcp-agent/claude-desktop.json`. Code: that example's server.yaml has `mcp.enabled` with stdio and no `tokenRef`, `apiKeyRef` or `allowAnonymous`. `sluice serve` exits 1 (run, internal/config/validate.go:99).
- cli.md:5-6. Doc: every command but `serve` is self-contained and read-only. Code: `mcp` builds the full runtime and runs queries (cmd/sluice/mcp.go:73). `budget show` creates `budget.db` when it is missing (cmd/sluice/budget.go:37, run).
- cli.md:92. Doc: `config validate --strict` rejects unknown YAML fields. Code: only for policy manifests (cmd/sluice/config.go:67-69). An unknown key in server.yaml passes (run).

### Broken code blocks

- cli.md:201. `sluice schema export --kind ColumnMaskPolicy` works, but the union form the section describes (`sluice schema export`) exits 1. See above.
- cli.md:217. `sluice audit verify ./data/audit --anchor "$SLUICE_AUDIT_GENESIS"`. In every example that env var holds the raw seed. The anchor must be the hex sha256 of the seed. With the seed the command reports a broken chain, exit 4 (run). With `sha256(seed)` it passes (run).
- mcp.md:124 is fine itself, but the "runnable variant" it points to does not start (see above).

### Proposed pages for this section

Keep all nine pages. No merge, no removal. Changes are about ownership and generation.

1. `reference/index.md`. Link table only (page, one line on what it holds). Drop counts and the long admonition. One sentence says which pages are generated.
2. `reference/cli.md`. Generate the command, flag and default tables from the cobra tree. Keep by hand: exit code table, one example per command. Owns all CLI flags (mcp.md links here).
3. `reference/configuration.md`. Generated. Must list sub-keys of `rest.tls`, `admin.tls`, `audit.file`, `audit.syslog`, `audit.s3`, `approval.webhooks[]`, give allowed values, and state the real env rule. Move the Secrets prose to operations/server-config.md and link.
4. `reference/policy-schema.md`. Generated. Fix required column, list item shapes, enum values, apiVersion per kind. Drop `AuditSink` or mark it unused.
5. `reference/rest-api.md`. Hand-written. Routes, auth, request and response fields, headers. Links to error-codes.md for the status table.
6. `reference/admin-api.md`. Hand-written. Routes, auth, response shapes. Drop the TLS warning, link to metrics.md for `/metrics`.
7. `reference/mcp.md`. Hand-written. Tools with input and output, identity per transport, visibility rules. Flags move to cli.md.
8. `reference/metrics.md`. Generated. Only metrics that are registered. Add a line on `go_*` and `process_*`.
9. `reference/error-codes.md`. Generated. Owns the code to HTTP status table. Add a note on policy `errorCode`.

The four "Regenerating" footers are the same text. Keep one sentence in index.md.

### Features the code has that the section does not document

- CLI: `sluice completion bash|fish|powershell|zsh` and `sluice help` (cobra built-ins, run).
- CLI: `sluice mcp --transport streamable_http` listens on `mcp.listen` from server.yaml. There is no flag. When unset it binds `:80` (mcp/server.go:188, run). Same for `serve`.
- CLI and config: `mcp.transport` also accepts `http`, `streamable-http`, `streamablehttp` (mcp/server.go:95).
- CLI: a missing policy directory is not an error. `policy validate ./nope` prints "policies OK (0 objects)" and exits 0, `policy explain` answers default-deny (internal/config/policies.go:157, run).
- CLI: `datasource check` exits 2 when no DataSource manifest exists and 1 when the named source is unknown (cmd/sluice/datasource.go:87, :113, run).
- Config: sub-keys missing from the page: `rest.tls.*` and `admin.tls.*` (`certFile`, `keyFile`, `clientCA`, `clientAuth`), `audit.file.*` (`path`, `rotateDaily`, `rotateSizeMB`, `genesis`), `audit.syslog.*` (`network`, `address`, `facility`, `tag`), `audit.s3.*` (12 keys), `approval.webhooks[].url|headersRef|timeout` (internal/config/server.go:140-172, 261-285).
- Config: validation rules in internal/config/validate.go (TLS block needs cert and key, tracing endpoint when enabled, `tracing.protocol` grpc or http, `sampleRatio` 0..1, syslog and s3 required fields, stdio MCP needs a credential).
- Config: without `audit.file.path` the audit log goes to `$TMPDIR/sluice-audit` with a warning (cmd/sluice/auditwire.go:46, run).
- Config: `approval.syncWait` is cut to `rest.requestTimeout` minus 2 s (cmd/sluice/approval.go:178-187).
- Config: `policies.engine` values `yaml`, `opa`, `composite`; composite members `yaml`, `opa`, `rebac` (cmd/sluice/policyengine.go:23-82). `RelationshipPolicy` is only evaluated with `composite` plus member `rebac`.
- Policy schema: `metadata.name` must be an RFC 1123 label, `metadata.priority` 0..1000 (pkg/apitypes/decoder.go:222, :228).
- Policy schema: `SubjectBinding.claims` takes extra free keys (pkg/apitypes/subjectbinding.go:36). The generator skips inline fields.
- Policy schema: shapes of list items are not on the page: clause `subjects` / `resources` (pkg/apitypes/common.go:13-58), `conditions[]`, `apiKeys[]`, `reject.rules[]`, `when.predicates[]`, `checks[]`, `rules[]`. Enum values are not listed either.
- Metrics: `/metrics` also exports `go_*` (29 families) and `process_*` (9) from the default registry (internal/telemetry/metrics.go:16, run).
- Metrics: approval metrics exist only when the approval broker is built (internal/approval/broker.go:100). `sluice_rest_rate_limited_total` appears after the first refusal.
- REST: other `Accept` values and unknown `format` values give 400 `ERR_SYNTAX` (rest/query.go:102, :117, run).
- REST: `/v1/query` is rate limited before auth by `limits.globalRps` and `limits.perIpRps`, 429 `ERR_RATE_LIMITED` (rest/ratelimit.go:43-64, run).
- Admin: `/admin/subjects/explain` accepts one, two or three part table names (admin/subjects.go:70-81).
- MCP: tool errors carry `policy`, `query_id` and `details` in the text (mcp/errors.go:24-48). `ERR_APPROVAL_PENDING` carries `approval_id` and `expires_at` in `details` (run).
- MCP: unknown approval id returns `ERR_APPROVAL_EXPIRED` "approval not found" (internal/queryservice/approval.go:125, run).
- MCP: no resources and no prompts are registered (run: `resources/list`, `prompts/list` empty).

## docs/reference/index.md

Prose words now: 196. Suggested after rewrite: 60.

A link table is enough. It needs no more than one line per page and one sentence on generated pages.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-8 | Pages are the source of truth for every external interface | fluff | |
| 2 | 10-15 | Four pages are produced by `make docs-generate`, hand edits get overwritten | implemented (Makefile:128-132) | |
| 3 | 13-14 | Hand edits make `make docs-check` fail in CI | not implemented | Target works locally (run, exit 0). No workflow runs it. |
| 4 | 19 | CLI: all 12 command paths | implemented (cmd/sluice/root.go:20-29) | 12 leaf commands (run). Count goes stale. `completion`, `help` not counted. |
| 5 | 20 | REST: `POST /v1/query`, approval endpoints, meta endpoints, error envelope | implemented (rest/routes.go:22-48) | |
| 6 | 21 | Admin: the `:9091` plane, including `/metrics` | implemented (internal/config/server.go:303, admin/routes.go:26) | |
| 7 | 22 | MCP: nine tools over stdio / Streamable HTTP | implemented (mcp/tools.go:23-85) | 9 tools on both transports (run). |
| 8 | 23 | Policy schema page: JSON Schema 2020-12 for all 11 kinds | mismatch | Page holds Go struct field tables. `schema export` union fails (run). |
| 9 | 24 | Configuration: fields, defaults, env overrides | implemented (scripts/gen-config-docs.go:81-90) | Env column is partly wrong, see configuration.md. |
| 10 | 25 | Metrics: Prometheus surface on the admin listener | implemented (admin/routes.go:26) | |
| 11 | 26 | Error codes: the 23-code catalogue | implemented (pkg/errors/codes.go:136-161) | Count goes stale. |
| 12 | 30-33 | Test suites and policy semantics live in other sections | duplicate | Links only. Owners: policies/testing.md, policies/index.md. |

### Code blocks

None.

## docs/reference/cli.md

Prose words now: 904. Suggested after rewrite: 550.

Can the page be generated: yes for names, aliases, `Use`, `Short`, flags, types and defaults. A walk over `newRootCmd().Commands()` with `Flags().VisitAll` needs about 60 lines.
What blocks it today:
- `newRootCmd` is in package `main` (cmd/sluice/root.go:12), so a `scripts/gen-*.go` program cannot import it. Two ways out: a golden test in `cmd/sluice` that renders the page and compares (tests already call `newRootCmd()`, cmd/sluice/version_test.go:12), or move the tree to an importable package.
- A generator that imports the command tree links DuckDB through cgo. The other generators avoid that.
- `cobra/doc` needs `github.com/cpuguy83/go-md2man/v2` as a real dependency. go.mod does not have it. A hand-written walk needs nothing new.
- Not in the tree as data: exit codes (free text in `Long`, missing for `budget show`, `version`, `apikey hash`, `schema export`), examples (no command sets `Example`), required flags (only `policy explain --user` uses `MarkFlagRequired`), the `SLUICE_MCP_TOKEN` fallback.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5 | Single binary with 12 command paths | implemented (cmd/sluice/root.go:20-29) | (run) `--help` walk. Count is fluff. |
| 2 | 5-6 | `serve` is the composition root, all other commands are self-contained and read-only | mismatch | `mcp` builds the runtime and runs queries (cmd/sluice/mcp.go:73). `budget show` creates `budget.db` (cmd/sluice/budget.go:37, run). |
| 3 | 10-13 | `--config` has no default search path; missing file is fine; malformed file is fatal | implemented (internal/config/server.go:392-401) | (run) missing: exit 0. Malformed: exit 1. |
| 4 | 15-17 | Every server.yaml field has a `SLUICE_` override, `.` maps to `__` | mismatch | See summary. Owner: configuration.md. |
| 5 | 19-27 | Exit codes 0, 1, 2, 3, 4 | implemented (cmd/sluice/main.go:19-32, serve.go:46, :125, config.go:57, datasource.go:142, audit.go:61) | (run) 1, 2, 3, 4 all seen. |
| 6 | 31-32 | `serve` starts REST always, MCP and admin when enabled | implemented (cmd/sluice/serve.go:90-113) | (run) |
| 7 | 32-33 | SIGINT/SIGTERM shut down gracefully, SIGHUP reloads policies | implemented (cmd/sluice/serve.go:41, :63-78) | SIGHUP (run). `serve --help` does not list SIGHUP. |
| 8 | 33 | `serve` exits 1 on startup failure, 2 on transport error | implemented (cmd/sluice/serve.go:46, :125) | (run) bad policy dir: 1. Port in use: 2. |
| 9 | 33-36 | Builds with `-tags=pure_parser` refuse to start, exit 1 | implemented (cmd/sluice/bootstrap.go:85-87) | Not run, needs another build. |
| 10 | 38-41 | `serve` flags `--config`, `--policies-dir`, no defaults | implemented (cmd/sluice/serve.go:131-132) | (run) help. |
| 11 | 49-52 | `mcp` runs only MCP, stdio by default; static credential checked once and pinned; `streamable_http` checks every request; exit 1 on startup or auth failure | implemented (cmd/sluice/mcp.go:83-127, mcp/identity.go:24-46) | (run) both transports, no credential: exit 1. |
| 12 | 54-61 | `mcp` flags and defaults; `--jwt` falls back to `SLUICE_MCP_TOKEN` | implemented (cmd/sluice/mcp.go:89-92, :131-136) | (run) |
| 13 | 69-73 | `version` prints version, commit, build time, Go version; `--json` | implemented (cmd/sluice/version.go:14-38) | (run) Also prints dirty flag, platform, parser. JSON keys are Go names (`Version`, `CommitFull`, ...). |
| 14 | 81-87 | `config validate [<policy-dir>]` checks server config and manifests; unenforceable settings give exit 3, same check as `serve`; exits 0/1/3 | implemented (cmd/sluice/config.go:46-104, bootstrap.go:97) | (run) `datasources.reload`, vault ref: 3. |
| 15 | 89-92 | `config validate` flags; `--strict` rejects unknown YAML fields | mismatch | Only for manifests (cmd/sluice/config.go:67-69). Unknown server.yaml key passes (run). |
| 16 | 100-103 | `policy validate` runs load plus compile, exits 0/1/3 | implemented (cmd/sluice/policy.go:50-81) | (run) `rewrite.hint`: 3. Missing dir: 0. |
| 17 | 105-107 | `policy validate --strict` | implemented (cmd/sluice/policy.go:85) | (run) |
| 18 | 115-121 | `policy explain` builds a synthetic subject; target is `--table`, `--sql` or both; `--user` plus one target required; exits 0, 1, 3 | implemented (cmd/sluice/policy.go:117-221) | (run) all cases. Tested: cmd/sluice/policy_explain_test.go:43-94. Invalid manifests give 3 too (policy.go:134-140). |
| 19 | 123-133 | `policy explain` flags and defaults | implemented (cmd/sluice/policy.go:224-234) | (run) `--table` needs exactly three parts (policy.go:260-264). |
| 20 | 143-146 | `policy test` compiles and runs suites; exit 3 on compile failure, case failure or zero cases | implemented (cmd/sluice/policytest.go:46-96) | (run) examples/policies: 4 passed. Missing tests dir: exit 1. |
| 21 | 148-152 | `policy test` flags, `--tests` default `<policy-dir>/tests` | implemented (cmd/sluice/policytest.go:66-69, :99-101) | |
| 22 | 158-164 | `datasource check [name]` (alias `ds`) reports factory and filters, no live probe; exit 1 on I/O error, 2 when a source fails | implemented (cmd/sluice/datasource.go:22, :64-144) | (run) Also 2 for "no manifests", 1 for unknown name. |
| 23 | 166-169 | `datasource check` flags `--dir` (default `./policies.d`), `--json` | implemented (cmd/sluice/datasource.go:149-150) | Help text names a `--policies-dir` fallback that does not exist (datasource.go:48). |
| 24 | 177-179 | `apikey hash` prints hex HMAC-SHA256 of `id:material` under the pepper | implemented (cmd/sluice/apikey.go:35-69) | (run) reproduces the hello-sluice hash. Tested: cmd/sluice/apikey_test.go:18. |
| 25 | 181-185 | `apikey hash` flags, all required, pepper raw or `secret://` | implemented (cmd/sluice/apikey.go:52-54, :65-67, :74-83) | (run) |
| 26 | 193-195 | `schema export` prints JSON Schema 2020-12, oneOf union of 11 kinds or one kind | mismatch | Union and `--kind RowFilterPolicy` exit 1 (run). See summary. |
| 27 | 197-199 | `schema export --kind` | implemented (cmd/sluice/schema.go:60-70, :93) | (run) 10 of 11 kinds. |
| 28 | 207-210 | `audit verify <dir>` walks `*.jsonl` in order, carries the hash across files; exit 4 on broken chain | implemented (cmd/sluice/audit.go:46-79) | (run) OK: 0. Tampered copy: 4. Missing dir: 1. |
| 29 | 212-215 | `audit verify` flags; `--anchor` is `sha256(seed)` | implemented (cmd/sluice/audit.go:82-83) | (run) |
| 30 | 223-225 | `budget show <subject>` reports CPU seconds and rows for one UTC day, default today | implemented (cmd/sluice/budget.go:25-61) | (run) `--day` is not checked; a bad value prints zeros. |
| 31 | 227-231 | `budget show` flags, `--state-dir` default `./state` | implemented (cmd/sluice/budget.go:57-59) | (run) Fails with exit 1 when the dir does not exist. |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 43 | console | `sluice serve --config ... --policies-dir ...` | untested, verified by hand | |
| 63 | console | `sluice mcp ... --jwt "$SLUICE_MCP_TOKEN"` | untested, verified by hand | Run with `--api-key` and with a bad token in the env var (exit 1). No valid JWT at hand. |
| 75 | console | `sluice version --json` | untested, verified by hand | Plain form is tested (cmd/sluice/version_test.go:11). |
| 94 | console | `sluice config validate ./policies.d --config sluice.yaml --strict` | untested, verified by hand | Close test: cmd/sluice/config_test.go:14. |
| 109 | console | `sluice policy validate ./policies.d --strict` | untested, verified by hand | |
| 135 | console | two `sluice policy explain` calls | untested, verified by hand | Close test: cmd/sluice/policy_explain_test.go:43. |
| 154 | console | `sluice policy test ./policies.d --json` | untested, verified by hand | Run on examples/policies. |
| 171 | console | `sluice ds check warehouse --dir ./policies.d` | untested, verified by hand | Run with an existing source name. |
| 187 | console | `sluice apikey hash --pepper secret://env/SLUICE_PEPPER ...` | untested, verified by hand | |
| 201 | console | `sluice schema export --kind ColumnMaskPolicy > file` | untested, verified by hand | This kind works. Union form is broken. |
| 217 | console | `sluice audit verify ./data/audit --anchor "$SLUICE_AUDIT_GENESIS"` | broken | Seed as anchor gives exit 4. Needs hex sha256 of the seed. |
| 233 | console | `sluice budget show alice --day 2026-07-06` | untested, verified by hand | Needs an existing `./state`. |

## docs/reference/rest-api.md

Prose words now: 675. Suggested after rewrite: 450.

Routes the server registers (rest/routes.go:22-47): `GET /v1/health`, `GET /v1/ready`, `GET /v1/version`, `GET /openapi.json` (no auth); `POST /v1/query` (rate limit, then auth); with an approval broker: `GET|POST /v1/approvals/{id}/accept`, `GET|POST /v1/approvals/{id}/reject` (token only), `GET /v1/approvals/{id}` (auth). The page lists all of them. All were called on a local server.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5 | Listens on `rest.listen`, default `:8080` | implemented (internal/config/server.go:292) | |
| 2 | 5-8 | Each request gets `X-Request-Id`, a body cap (`rest.maxBodyBytes`, 1 MiB), a timeout (`rest.requestTimeout`, 30 s) | implemented (rest/routes.go:59-62, internal/config/server.go:293-294) | (run) id echoed, 413 on a 1.1 MB body. |
| 3 | 8 | Panic recovery | internal detail | rest/routes.go:105 |
| 4 | 12-13 | `POST /v1/query` runs one statement through the pipeline and streams the result | implemented (rest/query.go:23-86) | (run) |
| 5 | 15-21 | Auth: `Authorization: Bearer <jwt>`, `X-Api-Key: <id>.<material>`, `Authorization: ApiKey ...` | implemented (internal/identity/jwt.go:317, internal/identity/apikey.go:187) | (run) both API key forms. JWT not run. |
| 6 | 23-24 | Missing or invalid credential: 401 `ERR_UNAUTHORIZED` plus `WWW-Authenticate: Bearer` | implemented (internal/identity/middleware.go:61-72) | (run) |
| 7 | 26 | Unknown body fields are rejected | implemented (rest/query.go:28) | (run) 400 `ERR_SYNTAX`. |
| 8 | 30 | `sql` string, required | mismatch | No check. `{}` gives 403 `ACL_DENIED` (run). |
| 9 | 31 | `params` not supported, `$n` refused with `ERR_UNSUPPORTED_SYNTAX` | not implemented | Refusal at internal/queryservice/execute.go:155 (run). |
| 10 | 32 | `max_rows` default `limits.maxRows`, cut to `limits.maxRowsCeiling` | implemented (internal/queryservice/execute.go:100-105) | (run) cap of 1 gave one row. |
| 11 | 33 | `timeout_ms` cut to `limits.maxQueryTimeout` | implemented (internal/queryservice/execute.go:94-99) | |
| 12 | 34 | `format`: `json` default, `csv`, `arrow` rejected | implemented (rest/query.go:91-105) | (run) |
| 13 | 35 | `meta` goes to the audit record as `client_meta`; caps 16 keys, 64 byte keys, 256 byte values | implemented (internal/queryservice/audit_helpers.go:50-52) | (run) `client_meta` seen in the audit file. |
| 14 | 37-38 | Without `format` the `Accept` header decides | implemented (rest/query.go:106-117) | (run) |
| 15 | 40-50 | JSON response: `query_id`, `columns`, `rows`, `row_count`, `truncated`, streamed | implemented (rest/render_json.go:27-108) | (run) Tested: rest/server_test.go:294. |
| 16 | 52-57 | Truncated result adds `warning` with `ERR_RESULT_TRUNCATED` | implemented (rest/render_json.go:95-100) | (run) Tested: rest/server_test.go:258. |
| 17 | 59-64 | CSV: `text/csv` with header row; `X-Sluice-Row-Count`, `X-Sluice-Truncated`, `X-Sluice-Warning` as HTTP trailers | implemented (rest/render_csv.go:21-67) | (run) with `curl --raw`. Tested: rest/server_test.go:199. |
| 18 | 60-62, 66-71 | Why trailers, client and proxy advice | internal detail | General HTTP behavior. One sentence is enough. |
| 19 | 73-75 | `X-Query-Id` on responses and on errors with an id; `X-Sluice-Applied-Policies` lists applied policies | implemented (rest/query.go:65-68, rest/errors.go:33-35) | (run) |
| 20 | 77-80 | Arrow output is declared but rejected | not implemented | rest/query.go:98, :113 (run). |
| 21 | 90-91 | Approval endpoints exist only when the approval workflow is configured | implemented (rest/routes.go:42-48) | (run) 404 without, working with. |
| 22 | 95-97 | Accept/reject: token in `?token=` or `X-Approval-Token` is the only authorization | implemented (rest/approvals.go:93-98) | (run) both. |
| 23 | 99-100 | Unknown id and bad token give the same 404 | implemented (rest/approvals.go:41-44) | (run) Tested: rest/approvals_test.go:88. |
| 24 | 101-102 | Same verb again: 200 "already decided"; other verb: 409 | implemented (rest/approvals.go:45-61) | (run) |
| 25 | 103-106 | `HEAD` and prefetch headers get 204 and change nothing | implemented (rest/approvals.go:102-112) | (run) HEAD and `Purpose: prefetch`. |
| 26 | 115-117 | `GET /v1/approvals/{id}`: auth needed, owner only, else 404; returns `approval_id`, `state`, `expires_at` | implemented (rest/approvals.go:69-82) | (run) |
| 27 | 123 | `/v1/health`: always 200, `status`, `version` | implemented (rest/health.go:35-40) | (run) |
| 28 | 124 | `/v1/ready`: 200 or 503 `degraded`, lists datasources `name, type, healthy, error` | implemented (rest/health.go:45-72) | 200 (run). 503 tested: rest/server_test.go:77. |
| 29 | 124 | Sources report unhealthy until the first sweep | internal detail | cmd/sluice/bootstrap.go:193-196 |
| 30 | 125 | `/v1/version`: `version, commit, build_time, go, parser_version` | implemented (rest/version.go:22-31) | (run) |
| 31 | 126, 128-131 | `/openapi.json` returns a stub; a generated spec is planned | not implemented | Stub at rest/version.go:36-50 lists 4 paths, no approval paths (run). |
| 32 | 135-137 | Every 4xx/5xx from query and meta endpoints is a JSON `APIError` | mismatch | 404 and 405 from the router are plain text (run). |
| 33 | 139-149 | Envelope: `code`, `message`, optional `query_id`, `policy`, `details` | implemented (pkg/errors/api_error.go:14-20) | (run) `policy` seen on a deny policy. |
| 34 | 149-159 | Status per code (202, 401, 403, 410, 413, 429) | duplicate | Correct (pkg/errors/codes.go:62-86). Owner: error-codes.md. |
| 35 | 161 | "Full 23-code catalogue" | fluff | Count goes stale. |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 42 | json | JSON response body | tested | rest/server_test.go:294 (`TestHandleQuery_JSONHappyPath`). Real run matches. |
| 55 | json | `warning` object | tested | rest/server_test.go:258. Real run matches. |
| 82 | console | `curl` to `/v1/query` with `X-Api-Key` | untested, verified by hand | Run with the hello-sluice key. |
| 108 | console | `curl -X POST .../accept?token=` with output | untested, verified by hand | Real key order is `approval_id`, `note`, `state`. Same data. |
| 139 | json | error envelope | untested, verified by hand | Shape matches. `"details": {}` never appears: empty details are left out. Real default-deny message is "no SqlAccessPolicy matched (default-deny)". |

## docs/reference/admin-api.md

Prose words now: 614. Suggested after rewrite: 350.

Routes the server registers (admin/routes.go:18-26), all behind the bearer token: `GET /admin/policies`, `GET /admin/datasources`, `GET /admin/subjects/explain`, `POST /admin/reload`, `GET /admin/audit/tail`, `GET /admin/approvals`, `GET /admin/healthz`, `GET /admin/version`, `GET /metrics`. The page lists all nine. All were called on a local server.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-8 | Separate listener, off by default, `admin.enabled`, `admin.listen` default `:9091` | implemented (internal/config/server.go:301-304, cmd/sluice/bootstrap.go:417) | |
| 2 | 12-13 | One static bearer token `admin.token` on every endpoint | implemented (admin/auth.go:16-31) | (run) The token is a literal, `secret://` is not resolved. |
| 3 | 13 | Compared in constant time | internal detail | admin/auth.go:23 |
| 4 | 19-20 | Empty token turns auth off and logs a warning at boot | implemented (admin/server.go:108-110, admin/auth.go:18-21) | (run) |
| 5 | 21 | Failed auth: 401 with `WWW-Authenticate: Bearer realm="sluice-admin"` | implemented (admin/auth.go:33-38) | (run) |
| 6 | 22 | Responses carry `X-Admin-Request-Id` | implemented (admin/routes.go:34-43) | (run) |
| 7 | 24-28 | TLS on the admin listener is not implemented; `admin.tls` fails validation | mismatch | Works. See summary. Tested: admin/tls_test.go:16, internal/config/validate_test.go:69. |
| 8 | 30-34 | Never expose the admin listener; bind locally, set a token, use TLS | duplicate | Owner: security/hardening.md. |
| 9 | 38-39 | `/admin/policies`: `version`, `digest`, policies by kind (`name, priority, enforcement`), `warnings` | implemented (admin/policies.go:12-48) | (run) |
| 10 | 48-49 | `/admin/datasources`: list of `name, type, healthy, last_check, last_error, latency_ms, schema_pulled_at` | mismatch | Wrapped in `{"datasources":[...]}`; two fields are optional. (run) |
| 11 | 57-67 | `/admin/subjects/explain` parameters `user`, `table` (required), `issuer`, `groups`, `claims`, `simulated_sql` | implemented (admin/subjects.go:18-67) | (run) Missing `user` or `table`: 400 `ERR_SYNTAX`. |
| 12 | 67, 78 | `simulated_sql` is evaluated instead of the bare table | mismatch | Table is still required and added. (run) |
| 13 | 76-80 | Response mirrors `sluice policy explain --json`; deny with no matched policy means default-deny | implemented (pkg/apitypes/dto.go:60-72) | (run) Real: `matched: null`, one `rejected` entry with an empty name. |
| 14 | 84-87 | `POST /admin/reload`: validate then swap; `{"ok": true, "digest": ...}`; `ERR_CONFIG_INVALID` or `ERR_POLICY_INVALID`; old snapshot stays | implemented (admin/reload.go:20-44, cmd/sluice/bootstrap.go:394-406) | (run) both codes, HTTP 400. |
| 15 | 87-88 | Works with `policies.reload: false` | implemented (cmd/sluice/bootstrap.go:389-409) | Read only. Tested: cmd/sluice/bootstrap_watcher_test.go:20. |
| 16 | 98-99 | `/admin/audit/tail?n=`: NDJSON, default 100, max 1000, `n` below 1 is 400 | implemented (admin/audit.go:17-50) | (run) `n=2`, `n=0`, `n=abc`. |
| 17 | 105-106 | Oldest first, read across rotated files | implemented (internal/audit/tail.go) | Order (run). Rotation not run. |
| 18 | 106-108 | Flush before read, partial line skipped | internal detail | |
| 19 | 112-115 | `/admin/approvals`: `{"pending": [...], "count": N}`, no tokens, 501 without a broker | implemented (admin/approvals.go:15-27) | (run) both. |
| 20 | 119-122 | `/admin/healthz`: `status, version, config_digest, config_version`; 503 `degraded` when a source is unhealthy | implemented (admin/healthz.go:11-42) | 200 (run). 503 read only. |
| 21 | 126 | `/admin/version`: `version, commit, build_time, go` | implemented (admin/healthz.go:44-60) | (run) |
| 22 | 130-131 | `/metrics`: Prometheus text behind the same token | implemented (admin/routes.go:26) | (run) 401 without token. |
| 23 | 131 | "Configure the scrape with a credentials file" | mismatch | The YAML uses inline `credentials`. |
| 24 | 142 | Metric catalogue is in metrics.md | duplicate | Owner: metrics.md. |
| 25 | 146-148 | `/admin/approvals` answers 501 when no broker is wired | duplicate | Same as row 19. |
| 26 | 148-151 | `/admin/reload` and `/admin/audit/tail` 501 paths only matter for embedders | internal detail | admin/reload.go:21, admin/audit.go:18. Audit tail is always wired through the temp dir fallback (cmd/sluice/auditwire.go:45). |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 15 | console | `curl` `/admin/healthz` with bearer | untested, verified by hand | Close test: admin/server_test.go:286. |
| 41 | console | `curl` `/admin/policies` with output | untested, verified by hand | Shape and key order match. Close test: admin/server_test.go:131. |
| 51 | console | `curl` `/admin/datasources` | untested, verified by hand | |
| 71 | console | `curl` `/admin/subjects/explain?...` | untested, verified by hand | |
| 90 | console | `curl -X POST` `/admin/reload` | untested, verified by hand | Close test: admin/server_test.go:204. |
| 101 | console | `curl` `/admin/audit/tail?n=20` | untested, verified by hand | Close test: admin/server_test.go:264. |
| 133 | yaml | Prometheus scrape config | untested, not verified | No Prometheus here. Text above says "credentials file", YAML uses `credentials`. |

## docs/reference/mcp.md

Prose words now: 685. Suggested after rewrite: 400.

Registered at run time (run, `tools/list` over stdio and over streamable HTTP): `await_approval`, `check_approval`, `describe_table`, `execute_sql`, `explain_access`, `list_accessible_tables`, `list_catalogs`, `list_tables`, `whoami`. No resources, no prompts, no resource templates.
Input schemas (run): `execute_sql {sql*, row_limit}`, `list_catalogs {}`, `list_tables {catalog*, schema, limit, cursor}`, `describe_table {table*}`, `whoami {}`, `explain_access {table, sql}`, `list_accessible_tables {catalog, limit, cursor}`, `check_approval {approval_id*, timeout_seconds}`, `await_approval {approval_id*, timeout_seconds}`. `*` = required. All set `additionalProperties: false`.
Transports: stdio (`sluice mcp`, or `serve` with `mcp.enabled`), streamable HTTP on `mcp.listen`. Stdio: one identity fixed at start. HTTP: each request authenticated; 401 with `WWW-Authenticate: Bearer realm="sluice-mcp"`.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-6 | MCP server so agents query under the same policy engine | fluff | Intro. |
| 2 | 6-9 | Same pipeline as `POST /v1/query`; audit records carry `origin: mcp` | implemented (mcp/tools.go:336-343, internal/queryservice/service.go:191) | (run) `"origin":"mcp"` in the audit file. |
| 3 | 9 | "There is no MCP-only bypass" | fluff | |
| 4 | 15-18 | `sluice mcp` flags | duplicate | Owner: cli.md. |
| 5 | 19-21 | Inside `serve`: `mcp.enabled`, `mcp.transport`, `mcp.listen`, `mcp.sessionIdleMax` | implemented (cmd/sluice/bootstrap.go:365-387) | (run) streamable HTTP. `sessionIdleMax` is the HTTP idle timeout (mcp/server.go:193). |
| 6 | 21-26 | Serve stdio takes identity from `mcp.tokenRef` / `mcp.apiKeyRef`; else refuses to start unless `mcp.allowAnonymous`; `config validate` exits 3 | implemented (cmd/sluice/mcp.go:145-173, internal/config/validate.go:99-103) | Validate (run). Tested: cmd/sluice/mcp_test.go:34. |
| 7 | 32 | stdio: one identity resolved at startup and pinned; none and no opt-in means no start | implemented (cmd/sluice/mcp.go:87-106, mcp/server.go:145-158) | (run) |
| 8 | 33 | streamable_http: every request authenticated; invalid credential 401; missing 401 unless allow-anonymous; session id alone gives nothing | implemented (mcp/identity.go:24-46) | (run) 401 with a valid session id and no key. Tested: mcp/auth_internal_test.go:64. |
| 9 | 35-36 | Anonymous sessions still hit default-deny | implemented (internal/policy/conflict.go:145) | (run) `--allow-anonymous`: `ACL_DENIED`. |
| 10 | 42-43 | `execute_sql`: input `sql`, `row_limit`; result `query_id, columns, rows, row_count, truncated` plus a text table | implemented (mcp/tools.go:320-381) | (run) |
| 11 | 42 | `row_limit` range 1..100000 | mismatch | Not enforced. See summary. |
| 12 | 44-45 | Policy errors come back as tool errors with the REST codes | implemented (mcp/errors.go:24-48) | (run) `ACL_DENIED`, `ERR_APPROVAL_PENDING`. |
| 13 | 49-51 | `list_catalogs`: no input, `{catalogs: [{name, type, healthy}]}`, not paged | implemented (mcp/tools.go:398-421) | (run) |
| 14 | 55-60 | `list_tables`: `catalog`, `schema`, `limit` (500, max 1000), `cursor`; stable order; `next_cursor` | implemented (mcp/tools.go:424-457, internal/queryservice/tables.go:137-209) | (run) paging, bad cursor gives `ERR_SYNTAX`. |
| 15 | 58 | `list_tables` returns the tables of the catalog | mismatch | Only tables in the schema cache. Empty on a fresh server. (run) |
| 16 | 64-66 | `describe_table`: `table`, three part or two part; result `table, columns[name, type, nullable]` | implemented (mcp/tools.go:460-520) | (run) Two part name parsed, but denied when policies name a schema. |
| 17 | 70-71 | `whoami`: `anonymous, subject, issuer, email, groups, auth_method` | implemented (mcp/tools.go:143-169) | (run) Tested: mcp/auth_internal_test.go:33. |
| 18 | 75-79 | `explain_access`: `table` or `sql`; result `subject, resource, decision, row_filters, column_masks, matched, rejected`; runs no query | implemented (mcp/tools.go:172-236) | (run) both inputs. |
| 19 | 78-79 | Agents should call it before `execute_sql` | fluff | Advice. Owner: cookbook/mcp-agents.md. |
| 20 | 83-85 | `list_accessible_tables`: `catalog`, `limit`, `cursor`; tables the identity may query | mismatch | Same cache limit as row 15. (run) |
| 21 | 89-92 | `check_approval`: `approval_id`; `approval_id, state, expires_at`; states pending, approved, rejected, expired; rerun the identical query | implemented (mcp/tools.go:101-108) | Unknown id (run). Approved flow run over REST. |
| 22 | 96-99 | `await_approval`: blocks until decided or timeout; cap 55 s | implemented (mcp/tools.go:110-121) | Unknown id (run). Cap read only. |
| 23 | 103-109 | Only an explicit Deny hides a table; reject rules and approval gates still list | mismatch | Any deny outcome hides, default-deny included. (run) |
| 24 | 107-109, 111-112, 114-116 | Reasons for the three visibility rules | internal detail | Design reasons. |
| 25 | 110-112 | `describe_table` returns all columns, masked ones too | implemented (internal/queryservice/tables.go:225-234) | (run) `email` listed. |
| 26 | 113-114 | Catalogs with no known tables stay visible in `list_catalogs` | implemented (internal/queryservice/tables.go:98-125) | (run) |
| 27 | 120-122 | Claude Desktop config path, absolute paths needed | fluff | Third-party behavior, cannot be checked against this code. |
| 28 | 143-144 | `env` feeds `secret://env/...` references | implemented (internal/secrets/env.go) | (run) pepper from env. |
| 29 | 144-146 | Runnable `sluice serve` variant in `examples/mcp-agent/claude-desktop.json` | mismatch | `serve` exits 1 with that example. (run) |
| 30 | 148-149 | Walkthrough in cookbook/mcp-agents.md | duplicate | Link only. |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 124 | json | Claude Desktop `mcpServers` entry with `sluice mcp --config= --policies-dir= --api-key=` | untested, verified by hand | Same command run over stdio with a python JSON-RPC client: `initialize` and `tools/list` work. Not run inside Claude Desktop. |

## docs/reference/policy-schema.md

Prose words now: 896. Suggested after rewrite: 1100 (tables grow, intro under 60 words).

### Generator: scripts/gen-policy-docs.go

- Reads: reflection on the real spec structs in `pkg/apitypes` (yaml tags, Go types). Fields are from code.
- Hand-kept list in the script: `kindSpecs` (lines 70-86), 11 kinds with a hand-written summary each. Compared with the registry (pkg/apitypes/decoder.go:41-52): same 11 kinds, none missing, none extra. A new kind needs a script edit, so the footer claim "tracks every new field" holds for fields only.
- Hard-coded prose: intro (JSON Schema claim, `schema export` claim), the envelope YAML, the footer with the drift gate claim.
- Can drift from code: field rows cannot (the check passes). Wrong by construction: the "required" column (from `omitempty`), empty types for `any`, no expansion of list items, inline maps skipped, no enum values, one apiVersion for all kinds, no knowledge of fields the runtime ignores.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 6-7 | Policies are YAML validated against a JSON Schema from `pkg/apitypes` | mismatch | Go struct decode plus Go checks (pkg/apitypes/decoder.go:162, :216). |
| 2 | 7-8 | The schema is bundled in the binary | mismatch | Built at run time (cmd/sluice/schema.go:65). |
| 3 | 8-9 | `sluice schema export` emits a oneOf union; `--kind` narrows | mismatch | Union exits 1. Owner: cli.md. (run) |
| 4 | 13-25 | Envelope: `apiVersion`, `kind`, `metadata` with `name`, `namespace`, `labels`, `annotations`, `priority`, `spec` | implemented (pkg/apitypes/typemeta.go:35-47) | Full envelope validates (run). `labels` and `annotations` are not read by any code. |
| 5 | 16 | `apiVersion: sluice.bino.bi/v1alpha1` for every kind | mismatch | Two kinds need v1beta1. (run) |
| 6 | 23 | `priority`: int32, higher wins ties | duplicate | Owner: policies/matching.md. Range 0..1000 (pkg/apitypes/decoder.go:228). |
| 7 | 33, 45-71 | `DataSource` and 18 rows `type` to `attachMode` | implemented (pkg/apitypes/datasource.go:16-52, internal/datasource/registry.go:219-266) | Sqlite source (run). Other types read only. |
| 8 | 65 | `readonly` is required | mismatch | Omitting it passes (run). |
| 9 | 69-71 | `healthCheck`, `healthCheck.query`, `healthCheck.interval` | not implemented | No reader of `Spec.HealthCheck`. |
| 10 | 34, 73-98 | `SubjectBinding` and rows `issuer` to `apiKeys` without `groupsSource` | implemented (pkg/apitypes/subjectbinding.go:14-26, cmd/sluice/bootstrap.go:618-786) | API key binding (run). |
| 11 | 91 | `groupsSource` | not implemented | No reader. |
| 12 | 35, 100-116 | `SqlAccessPolicy` rows | implemented (pkg/apitypes/policy_access.go, internal/policy/conflict.go:120-141) | allow, deny, `message`, `errorCode` (run). |
| 13 | 36, 118-142 | `RowFilterPolicy` rows | implemented (pkg/apitypes/policy_rowfilter.go, pkg/apitypes/decoder.go:274) | Predicate filter (run). |
| 14 | 139-140 | `filter.predicate.value`, `.values` with empty type | mismatch | Type is `any` / list of `any`. Generator bug. |
| 15 | 37, 144-180 | `ColumnMaskPolicy` rows | implemented (pkg/apitypes/policy_columnmask.go, internal/policy/snapshot.go:498-505) | `null` mask (run). Per-type args are for the policies section. |
| 16 | 161 | `mask.args.value` with empty type | mismatch | Type is `any`. |
| 17 | 38, 182-197 | `QueryRejectPolicy` rows | implemented (pkg/apitypes/policy_queryreject.go, pkg/apitypes/decoder.go:411) | Empty `rules` refused (run). |
| 18 | 39, 199-219 | `QueryRewritePolicy` rows `limit`, `sample`, `timeout` | implemented (pkg/apitypes/policy_queryrewrite.go, internal/policy/snapshot.go:445-470) | `limit.max` range check (run). |
| 19 | 220 | `rewrite.hint` | not implemented | Refused at compile. (run) |
| 20 | 40, 222-239 | `ApprovalPolicy` rows | implemented (pkg/apitypes/policy_approval.go, pkg/apitypes/decoder.go:337) | Full approval flow (run). |
| 21 | 41, 241-262 | `RelationshipPolicy` rows | implemented (pkg/apitypes/policy_relationship.go, internal/rebac/engine.go:83-91) | Only evaluated with `policies.engine: composite` and member `rebac` (cmd/sluice/policyengine.go:73). Validation (run). Engine read only. |
| 22 | 42, 264-270 | `DataClassification`, `rules` | implemented (pkg/apitypes/dataclassification.go, internal/policy/snapshot.go:167-190) | Validation (run). |
| 23 | 43, 272-293 | `AuditSink` and 16 rows | not implemented | Decoded, never used. See summary. |
| 24 | all tables | Column "Notes": required / optional | mismatch | From `omitempty` only. See summary. |
| 25 | 296-303 | `make docs-generate` regenerates; reflection tracks every new field | implemented (Makefile:130, scripts/gen-policy-docs.go:141-200) | Kinds list is hand-kept. |
| 26 | 303-304 | Drift gate in `docs-deploy.yaml` fails on difference | not implemented | See summary. |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 15 | yaml | Shared envelope with placeholders | untested, verified by hand | A filled-in copy passes `policy validate --strict`. Not valid as written (placeholders). |
| 298 | bash | `make docs-generate` | untested, not verified | It writes into `docs/`. `make docs-check` run instead: exit 0. |

## docs/reference/metrics.md

Prose words now: 308. Suggested after rewrite: 180.

### Generator: scripts/gen-metrics-docs.go

- Reads: source text of every non-test `.go` file under `internal/` and `pkg/` with `go/ast`. Collects calls `telemetry.DefineCounter|DefineHistogram|DefineGauge` (name, help, labels) and `prometheus.NewCounter|NewHistogram|NewGauge` (name, help, no labels). No hand-kept metric list.
- Hard-coded prose: intro, the whole "Process" section with the `sluice_build_info` label list (lines 48-53), footer with the drift gate claim. The label list matches internal/telemetry/init.go:39 today but would not follow a change.
- Can drift from code: yes, in meaning. It lists definitions, not registrations. 9 of 18 rows never reach `/metrics`. It also misses `prometheus.NewCounterVec|NewGaugeVec|NewHistogramVec` calls (only `sluice_build_info` today) and the `go_*` / `process_*` collectors. Groups are Go package names, not metric subsystems.
- A check against the live registry would have caught this: start a server, scrape, compare names.

Live scrape (run), three servers: `sluice_build_info`, `sluice_audit_enqueued_total`, `sluice_audit_queue_depth`; with approvals: the four `sluice_approval_*`; after a rate limit refusal: `sluice_rest_rate_limited_total`. Types and labels match the page for all of these.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 6-7 | `/metrics` on the admin port, default `:9091` | implemented (admin/routes.go:26) | (run) Needs `admin.enabled` and the token. |
| 2 | 7-8 | Names follow `sluice_<subsystem>_<metric>` with `_total` / `_seconds` / `_bytes` | mismatch | No `_bytes` metric. `sluice_schema_stale_entries` is a counter without `_total`. |
| 3 | 10-12, 18-19 | Table comes from walking `telemetry.Define*` calls; why `build_info` is not in it | internal detail | |
| 4 | 16-17 | `sluice_build_info` with labels `version, commit, go, platform, parser, instance, environment` | implemented (internal/telemetry/init.go:35-65) | (run) gauge, value 1. `instance` and `environment` are always empty. |
| 5 | 27 | `sluice_approval_decisions_total` counter, `outcome` | implemented (internal/approval/metrics.go:27, broker.go:300) | (run) |
| 6 | 28 | `sluice_approval_pending` gauge | implemented (internal/approval/metrics.go:25) | (run) |
| 7 | 29 | `sluice_approval_wait_seconds` histogram | implemented (internal/approval/metrics.go:29) | (run) |
| 8 | 30 | `sluice_approval_webhook_deliveries_total` counter, `status` | implemented (internal/approval/metrics.go:32, webhook.go:98) | (run) `status="delivered"`. |
| 9 | 36 | `sluice_audit_dropped_total` counter, `sink`, `reason` | implemented (internal/audit/metrics.go:35, dispatcher.go:235) | Read only, no drop happened. |
| 10 | 37 | `sluice_audit_enqueued_total` counter, `event_type` | implemented (internal/audit/metrics.go:38, dispatcher.go:209) | (run) |
| 11 | 38 | `sluice_audit_queue_depth` gauge, `sink` | implemented (internal/audit/metrics.go:34, dispatcher.go:277) | (run) |
| 12 | 39 | `sluice_audit_rotate_duration_seconds` histogram, `sink` | not implemented | Never observed. |
| 13 | 40 | `sluice_audit_write_errors_total` counter, `sink` | implemented (internal/audit/metrics.go:36, dispatcher.go:268) | Read only. |
| 14 | 46 | `sluice_policy_denials_total` | not implemented | `RegisterMetrics` never called. (run) absent. |
| 15 | 47 | `sluice_policy_eval_duration_seconds` | not implemented | Same. |
| 16 | 48 | `sluice_policy_evaluations_total` | not implemented | Same. |
| 17 | 49 | `sluice_policy_rejections_total` | not implemented | Same. |
| 18 | 55 | `sluice_rest_rate_limited_total` counter, `scope` | implemented (rest/ratelimit.go:33, :58) | (run) `scope="global"`. |
| 19 | 61 | `sluice_schema_cache_hits_total` | not implemented | `RegisterMetrics` never called. (run) absent. |
| 20 | 62 | `sluice_schema_cache_misses_total` | not implemented | Same. |
| 21 | 63 | `sluice_schema_refresh_duration_seconds` | not implemented | Same. |
| 22 | 64 | `sluice_schema_stale_entries` | not implemented | Same. |
| 23 | 69-74 | `make docs-generate`; parses `internal/` and `pkg/` with `go/ast` | internal detail | scripts/gen-metrics-docs.go:77-85 |
| 24 | 75-76 | Drift gate in `docs-deploy.yaml` | not implemented | See summary. |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 69 | bash | `make docs-generate` | untested, not verified | Writes into `docs/`. `make docs-check` run instead: exit 0. |

## docs/reference/error-codes.md

Prose words now: 192. Suggested after rewrite: 200.

### Generator: scripts/gen-errorcodes-docs.go

- Reads: `pkgerr.AllCodes()`, `pkgerr.Status(c)`, `pkgerr.Message(c)` from the real package (lines 56-59). No list in the script.
- `AllCodes()` itself is a hand-kept slice (pkg/errors/codes.go:136-161) next to the const block (:15-52). Compared row by row: 23 constants, 23 entries in `AllCodes()`, 23 entries in each map, 23 rows on the page. All equal.
- Hard-coded prose: the three intro sentences and the footer.
- Can drift from code: a new constant that is added to neither `AllCodes()` nor the maps passes all tests (pkg/errors/codes_test.go:10, :42 check only list against maps) and would be missing from the page.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 6 | `pkg/errors` defines the machine-readable catalogue | internal detail | |
| 2 | 7-8 | Every API response, CLI message and audit record carries one of these codes | mismatch | See summary. |
| 3 | 8 | Codes never change silently; additions need a CHANGELOG entry | fluff | Process rule, not product behavior. |
| 4 | 12 | `ERR_SYNTAX` 400 | implemented (internal/queryservice/execute.go:548, rest/query.go:36) | (run) |
| 5 | 13 | `ERR_MULTIPLE_STATEMENTS` 400 | implemented (internal/queryservice/execute.go:534) | Read only. A deny wins over a parse error (execute.go:216), so `SELECT 1; SELECT 2` gave `ACL_DENIED` (run). |
| 6 | 14 | `ERR_UNSUPPORTED_SYNTAX` 400 | implemented (internal/queryservice/execute.go:156, rest/query.go:99) | (run) |
| 7 | 15 | `ERR_REWRITE_FAILED` 400 | implemented (internal/rewriter/errors.go:65) | Read only. |
| 8 | 16 | `ERR_MASK_UNSUPPORTED_CONTEXT` 400 | implemented (internal/rewriter/substitute_mask.go:57, internal/queryservice/maskedrows.go:90) | Read only. |
| 9 | 17 | `ERR_UNAUTHORIZED` 401 | implemented (internal/identity/middleware.go:68, admin/auth.go:37, mcp/identity.go:40) | (run) all three. |
| 10 | 18 | `ACL_DENIED` 403 | implemented (internal/queryservice/execute.go:475) | (run) |
| 11 | 19 | `ACL_REJECTED` 403 | implemented (internal/queryservice/execute.go:137, :151, :492) | Read only. |
| 12 | 20 | `ERR_DATASOURCE_UNAVAILABLE` 503 | implemented (internal/queryservice/execute.go:585) | Read only. |
| 13 | 21 | `ERR_CONFIG_INVALID` 400 | implemented (admin/reload.go:30) | (run) Only from `POST /admin/reload`. |
| 14 | 22 | `ERR_POLICY_INVALID` 400 | implemented (admin/reload.go:32) | (run) Only from `POST /admin/reload`. |
| 15 | 23 | `ERR_TIMEOUT` 504 | implemented (internal/queryservice/execute.go:583) | Read only. |
| 16 | 24 | `ERR_CANCELED` 499 | implemented (internal/queryservice/execute.go:581) | Read only. |
| 17 | 25 | `ERR_QUERY_FAILED` 400 | implemented (internal/queryservice/execute.go:587) | (run) |
| 18 | 26 | `ERR_RATE_LIMITED` 429 | implemented (rest/ratelimit.go:63, internal/queryservice/execute.go:65, :453) | (run) |
| 19 | 27 | `ERR_BUDGET_EXCEEDED` 429 | implemented (internal/budget/manager.go:138, :141) | Read only. |
| 20 | 28 | `ERR_PAYLOAD_TOO_LARGE` 413 | implemented (rest/query.go:32, internal/queryservice/execute.go:92) | (run) both. |
| 21 | 29 | `ERR_RESULT_TRUNCATED` 200 | implemented (rest/render_json.go:97, rest/render_csv.go:62) | (run) Never a response status of its own: `warning.code` in JSON, `X-Sluice-Warning` trailer in CSV. |
| 22 | 30 | `ERR_AUDIT_UNAVAILABLE` 503 | implemented (internal/queryservice/audit_helpers.go:221) | Read only. |
| 23 | 31 | `ERR_APPROVAL_PENDING` 202 | implemented (internal/queryservice/approval.go:107) | (run) |
| 24 | 32 | `ERR_APPROVAL_REJECTED` 403 | implemented (internal/queryservice/approval.go:95) | Read only. |
| 25 | 33 | `ERR_APPROVAL_EXPIRED` 410 | implemented (internal/queryservice/approval.go:101, :125) | (run) over MCP for an unknown id. |
| 26 | 34 | `ERR_INTERNAL` 500 | implemented (rest/errors.go:20, pkg/errors/api_error.go:126) | Read only. |
| 27 | 12-34 | HTTP column | implemented (pkg/errors/codes.go:62-86) | All 23 equal the map. A policy `errorCode` keeps the 403 of `ACL_DENIED` under the custom code (run). |
| 28 | 43-44 | Page comes from `pkg/errors.AllCodes()` | internal detail | scripts/gen-errorcodes-docs.go:56 |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 39 | bash | `make docs-generate` | untested, not verified | Writes into `docs/`. `make docs-check` run instead: exit 0. |

## docs/reference/configuration.md

Prose words now: 475. Suggested after rewrite: 900 (about 30 more key rows and a meaning column; prose under 80 words).

### Generator: scripts/gen-config-docs.go

- Reads: reflection on `config.ServerConfig` (mapstructure tags, Go types) and the values of `config.DefaultServerConfig()`. No hand-kept key list. All 77 rows and defaults are from code.
- Hard-coded prose: intro and env example, the sentence "every field can be overridden through the environment", the whole Secrets section, the "Reference schema" section, the footer with the drift gate claim.
- Computed, not read: the env column (`SLUICE_` + upper case path, line 184-187). The loader has its own hand-kept list of keys that env can reach (`setDefaults`, internal/config/server.go:419-496). The two lists differ.
- Can drift from code: rows and defaults cannot. Gaps by construction: pointer structs that are nil by default get one row and no sub-keys (lines 158-168), lists of structs get no item fields, there is no meaning or allowed-values column, and it does not know which keys the runtime ignores.

Runtime readers per key group (read from code): `rest.*` cmd/sluice/bootstrap.go:335-343; `mcp.*` bootstrap.go:365-376 and cmd/sluice/mcp.go:111-167; `admin.*` bootstrap.go:417-425; `duckdb.*` bootstrap.go:166-177; `datasources.failFast` bootstrap.go:158; `policies.directory` bootstrap.go:132; `policies.reload` cmd/sluice/serve.go:58; `policies.engine|composite|opa|rebac` cmd/sluice/policyengine.go:23-77; `audit.file|syslog|s3` cmd/sluice/auditwire.go:34-102; `audit.failClosed` bootstrap.go:306; `audit.sqlSampleBytes` bootstrap.go:318; `logging.*` bootstrap.go:108-110; `tracing.*` bootstrap.go:112-118; `identity.apiKeyPepper` bootstrap.go:645; `limits.*` bootstrap.go:148, 178, 265-266, 311-317, 354-361; `cache.rewrite.*` bootstrap.go:275-276; `approval.*` cmd/sluice/approval.go:37-77 and bootstrap.go:319, 327; `budget.*` cmd/sluice/budgetwire.go:20-41.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 6-7 | One `server.yaml` at boot plus policy YAML files under a policies directory | implemented (cmd/sluice/bootstrap.go:93, :130-136) | (run) The CLI help calls the file `sluice.yaml`. |
| 2 | 7-9 | `SLUICE_` env vars override fields, `.` maps to `__` | implemented (internal/config/server.go:388-390) | (run) `SLUICE_REST__LISTEN`, `SLUICE_LIMITS__GLOBALRPS`, `SLUICE_LOGGING__LEVEL`. |
| 3 | 18-20 | Every field can be overridden through the environment | mismatch | See summary. (run) |
| 4 | 24, 26-27 | `rest.listen`, `rest.maxBodyBytes`, `rest.requestTimeout` and defaults | implemented (internal/config/server.go:291-295) | (run) |
| 5 | 25 | `rest.tls` (`TLSConfig?`, unset) | implemented (cmd/sluice/bootstrap.go:342, rest/server.go:144) | Sub-keys missing on the page. `clientAuth` is only validated (internal/config/validate.go:130). Tested: rest/tls_test.go:38. |
| 6 | 28-34 | `mcp.enabled`, `transport`, `listen`, `sessionIdleMax`, `tokenRef`, `apiKeyRef`, `allowAnonymous` | implemented (internal/config/server.go:296-300) | (run) HTTP transport. Empty `listen` binds `:80`. |
| 7 | 35-37 | `admin.enabled`, `admin.listen`, `admin.token` | implemented (internal/config/server.go:301-304) | (run) |
| 8 | 38 | `admin.tls` | implemented (cmd/sluice/bootstrap.go:423) | (run) HTTPS on the admin port. Sub-keys missing on the page. |
| 9 | 39-44 | `duckdb.memoryLimit`, `threads`, `tempDir`, `maxOpen`, `maxIdle`, `connMaxIdle` | implemented (internal/config/server.go:305-311) | Read only. |
| 10 | 45 | `datasources.directory` default `./datasources.d` | not implemented | Never read. (run) |
| 11 | 46 | `datasources.failFast` default `true` | implemented (cmd/sluice/bootstrap.go:158) | Read only. |
| 12 | 47 | `datasources.reload` default `false` | not implemented | `true` is rejected. (run) |
| 13 | 48-49 | `policies.directory`, `policies.reload` | implemented (cmd/sluice/bootstrap.go:132, cmd/sluice/serve.go:58) | (run) |
| 14 | 50-55 | `policies.engine`, `composite.members`, `opa.moduleDir`, `opa.query`, `rebac.cacheTtl`, `rebac.cacheSize` | implemented (cmd/sluice/policyengine.go:23-82) | Read only. Allowed values not on the page. |
| 15 | 56-58 | `audit.file`, `audit.syslog`, `audit.s3` (one row each, unset) | implemented (cmd/sluice/auditwire.go:34, :56, :70) | File sink (run). 20 sub-keys missing on the page. |
| 16 | 59-60 | `audit.failClosed`, `audit.sqlSampleBytes` | implemented (cmd/sluice/bootstrap.go:306, :318) | `sql_sample` seen in audit records (run). |
| 17 | 61-63 | `logging.level`, `format`, `maxErrorBytes` | implemented (cmd/sluice/bootstrap.go:108-110) | (run) json format. |
| 18 | 64-68 | `tracing.enabled`, `endpoint`, `protocol`, `insecure`, `sampleRatio` | implemented (cmd/sluice/bootstrap.go:112-118, internal/config/validate.go:22-32) | Read only. |
| 19 | 69 | `identity.apiKeyPepper` | implemented (cmd/sluice/bootstrap.go:645) | (run) Must be a `secret://` URI; a plain value fails validation. |
| 20 | 70-76 | `limits.maxRows`, `maxRowsCeiling`, `maxSqlBytes`, `queryTimeout`, `maxQueryTimeout`, `maxConcurrent`, `disableCrossCatalog` | implemented (cmd/sluice/bootstrap.go:311-317) | (run) `maxSqlBytes` gives `ERR_PAYLOAD_TOO_LARGE`. |
| 21 | 77-83 | `limits.globalRps`, `globalBurst`, `perIpRps`, `perIpBurst`, `perIpMaxBuckets`, `defaultSubjectRps`, `defaultSubjectBurst` | implemented (cmd/sluice/bootstrap.go:265-266, :354-361) | (run) global bucket. |
| 22 | 84-86 | `cache.rewrite.enabled`, `size`, `ttl` | implemented (cmd/sluice/bootstrap.go:275-276) | Read only. |
| 23 | 87-95 | `approval.publicBaseUrl`, `webhooks`, `syncWait`, `requestTtl`, `grantTtl`, `maxPending`, `sqlSampleBytes`, `persist`, `stateDir` | implemented (cmd/sluice/approval.go:37-77) | (run) base URL, webhook, syncWait. Webhook item fields missing on the page. |
| 24 | 96-100 | `budget.enabled`, `stateDir`, `flushInterval`, `failClosed`, `retentionDays` | implemented (cmd/sluice/budgetwire.go:20-41) | Read only. |
| 25 | 24-100 | Env var column | mismatch | Computed by the script. Wrong for keys with no viper default and for struct or list keys. See summary. |
| 26 | 104-105 | All `*Ref` / `genesis` / `token` fields accept `secret://<provider>/<path>` | mismatch | Not `admin.token`. (run) |
| 27 | 107 | `secret://env/NAME` reads the environment | implemented (internal/secrets/env.go) | (run) |
| 28 | 108-109 | `secret://file/PATH`; group or world writable refused, world readable warned | implemented (internal/secrets/file.go:51-57) | Read only. The page writes "0o022 rejected", which means mode bits 022. |
| 29 | 111-112 | Vault, AWS SM, GCP SM come in v0.3+; rejected until then | not implemented | Rejection (run). |
| 30 | 104-112 | Secrets section as a whole | duplicate | Owner: operations/server-config.md. |
| 31 | 116-118 | `sluice schema export` covers every policy object | mismatch | Union exits 1. Owner: cli.md. |
| 32 | 122-127 | `make docs-generate`; reflection tracks every new field | implemented (Makefile:132, scripts/gen-config-docs.go:120-173) | |
| 33 | 127-128 | Drift gate in `docs-deploy.yaml` | not implemented | See summary. |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 11 | bash | `export SLUICE_REST__LISTEN=:8443` | untested, verified by hand | Run with another port: the server listened there. |
| 122 | bash | `make docs-generate` | untested, not verified | Writes into `docs/`. `make docs-check` run instead: exit 0. |
