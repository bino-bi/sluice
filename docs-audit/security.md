# Audit: Security

Binary: `/tmp/sluice-bin/sluice` (`dev`, commit 6f7b99f). "ran" means I ran it against a local server built from `examples/hello-sluice` (scratch: `/tmp/audit-security/`, ports 18200 to 18207). "read" means code only.

## Summary

Rows: 139 claims. implemented 87, mismatch 19, not implemented 4, internal detail 1, fluff 17, duplicate 11.
Code blocks: 12 plus 1 Mermaid diagram. tested 0, untested and verified by hand 10, untested and not verified 1, broken 1.

### Not implemented

- audit.md:27-28. Event types `policy-decision`, `config-reload`, `auth-event`, `datasource-connect`. Constants exist (internal/audit/record.go:23-27), nothing emits them (grep of `Event*` in non-test code). Ran: 5 policy reloads, 1 `POST /admin/reload`, 2 failed logins wrote no record.
- audit.md:127-140. `AuditSink` manifest kind. It parses and validates (pkg/apitypes/decoder.go:505), but `sluice serve` never reads it: `snap.AuditSinks` has one writer (internal/config/policies.go:131) and no reader. Ran: serve with a `type: file` manifest pointing at `/var/lib/sluice/audit` wrote to `audit.file.path` from the server config, the manifest path was never created.
- audit.md:143-146. `postgres` and `otlp` audit sinks. Declared (pkg/apitypes/auditsink.go:51,53), fail validation. The page says so already.
- hardening.md:57. Identity from the client certificate ("on the roadmap"). No code maps a certificate to a subject.

### Docs and code disagree

Security relevant first.

- hardening.md:63-64 and key-rotation.md:16,117-118. Doc: pass the admin token with `SLUICE_ADMIN__TOKEN` instead of writing it into `sluice.yaml`. Code: the env var is ignored when the `admin.token` key is absent from the YAML file (or there is no file), because `admin.token` has no viper default (internal/config/server.go:431-432). Ran: env set, key absent, log says `admin: running without an auth token`, `GET /admin/version` with no token or a wrong token returns 200. With `admin.token: ""` or any value in the YAML the env var wins (ran: 401 without, 200 with the env token).
- hardening.md:92-111. Doc: put `rateLimit` and `budget` on the binding, example given. Code: both specs are keyed by `spec.claims.subjectId` or `spec.issuer` only (cmd/sluice/bootstrap.go:688-693, cmd/sluice/budgetwire.go:70-75). The example sets neither. Ran the example (budget lowered to `rowsPerDay: 3`): 30 requests in about 1 s all returned 200, `sluice budget show report-bot` shows `rows=66`. A binding with `claims.subjectId` got 429 `ERR_RATE_LIMITED` after the burst and 429 `ERR_BUDGET_EXCEEDED`. Budgets also need `budget.enabled: true` (default false, internal/config/server.go:365). The page does not say so.
- hardening.md:148-151. Doc: roll out with `enforcementMode: DryRun` and watch the audit log for would-be denials. Code: a DryRun or Audit policy is not enforced (internal/policy/conflict.go:42-45), and nothing about it is written to the audit log (internal/queryservice/audit_helpers.go:97 copies only `dec.Applied`), the server log or metrics. Ran: DryRun deny on `orders`, query served, 0 hits for the policy name in audit dir and log. Shadow matches show only in `sluice policy explain`, `GET /admin/subjects/explain` (field `shadow`) and policy tests.
- index.md:16-17, audit.md:73-76. Doc: the access record is "durably enqueued" before the first row. Code: the gate is "the in-memory queue took the record within 200 ms" (internal/audit/dispatcher.go:206-230, internal/queryservice/audit_helpers.go:212-225). The file write is async, flushed every 200 ms (dispatcher.go:83, 264-274). A failed disk write only counts `sluice_audit_write_errors_total` and logs. Ran: the line count of the audit file grew about 0.5 s after the response arrived.
- audit.md:5-8. Doc: every terminal outcome appends at least one record. Code: no record for a failed login (401), a transport rate limit 429 (internal/transport/rest/ratelimit.go:57-64), `service saturated` (internal/queryservice/execute.go:51-53), or admin calls other than policy explain. Ran: two 401 responses, audit file unchanged.
- key-rotation.md:14,82,86-88. Doc: a new genesis seed plus restart starts a new chain, and a mixed directory will not verify. Code: the seed is read only when the audit directory has no records (internal/audit/dispatcher.go:104-129). Ran: restart with a new seed on a used directory wrote no second genesis record, the directory still verifies with the old anchor and fails with the new one (exit 4).
- key-rotation.md:13,75-78. Doc: HS JWT secrets (`hmacSecretRef`) take effect on restart, resolved once at startup. Code: resolved again on every policy reload (cmd/sluice/bootstrap.go:470-478). Ran: overwrote the secret file, `POST /admin/reload`, old token 401, new token 200, no restart.
- index.md:11-12. Doc: SELECT, EXPLAIN, SET, SHOW and PRAGMA reach the engine. Code: PRAGMA is on the allowlist (internal/parser/shape.go:29-36) but pg_query cannot parse it and no statement is classified as PRAGMA (internal/pgquery/stmt.go:14-62). Ran under an allow that matches: `PRAGMA database_list` returns 400 `ERR_SYNTAX`. `SET` reaches DuckDB and fails there with `ERR_QUERY_FAILED` (config is locked, internal/executor/harden.go:82). `SHOW TABLES`, `SELECT 1`, `EXPLAIN SELECT 1` are served. With table-scoped allows only, every statement without a table gets `ACL_DENIED`.
- index.md:21. Doc: credentials are never inlined. Code: `admin.token` is a plain string in `sluice.yaml` (internal/config/server.go:70), `examples/hello-sluice/server.yaml` inlines it.
- threat-model.md:64. Doc: DDL, COPY, ATTACH return `ERR_UNSUPPORTED_SYNTAX`. Code: policy runs before the statement gate (internal/queryservice/execute.go:216-223, internal/rewriter/rewriter.go:361-368). Ran: `COPY` and `CREATE TABLE ... AS SELECT` on an allowed table return 400 `ERR_UNSUPPORTED_SYNTAX`. `DROP TABLE`, `CREATE TABLE t(a int)` return 403 `ACL_DENIED` unless an allow matches. `ATTACH`, `INSTALL`, `LOAD` return 400 `ERR_SYNTAX` under an allow (pg_query does not parse them), 403 `ACL_DENIED` otherwise.
- threat-model.md:76. Doc: single-use capability URLs. Code: the URL can be called again (ran: second accept returns 200 `already decided`, reject after accept returns 409). Single-use is the grant: one run of the identical query (internal/approval/broker.go:330, ran: third run is pending again).
- threat-model.md:93-97. Doc: with a filter column the table lacks, a crafted query gets the rows unfiltered. Code: the filter column is qualified with the reserved alias `sluice_src` (internal/rewriter/inject_filter.go:22, 49-67, 407). Ran 6 crafted queries (outer column named like the filter column, LATERAL, alias and CTE named `sluice_src`): all refused (`ERR_QUERY_FAILED` or `ERR_UNSUPPORTED_SYNTAX`). Every plain read of such a table fails with `ERR_QUERY_FAILED`. True part: Sluice does not check the column at load time. I could not reproduce the leak.
- hardening.md:70-74. Doc: the `s3` `allowedPaths` list is the DuckDB external access carve-out. Code: the carve-out is the whole bucket, `s3://<bucket>/` (internal/datasource/drivers/s3/driver.go:174-178). For `http` it is the exact URL list (internal/datasource/drivers/httpfile/driver.go:151-155). Read only.
- audit.md:17. Doc: `subject.method` is jwt, api_key, admin_token or none. Code: only `jwt` (internal/identity/jwt.go:310) and `api_key` (internal/identity/apikey.go:175) are set. `admin_token` and `none` are constants with no user (internal/identity/userctx.go:20-21). `remote_ip` holds `host:port` (ran: `127.0.0.1:57306`).
- audit.md:22. Doc: `duration_ms` only on `query-result`. Code: set on every query record (internal/queryservice/audit_helpers.go:197, 213). Ran: `query` record with `duration_ms: 26`.
- audit.md:24. Doc: every record has `sluice_version`, `duckdb_version`, `parser_version`. Code: `sluice_version` on all (internal/audit/dispatcher.go:193-195), `parser_version` on the genesis record only (dispatcher.go:161), `duckdb_version` is never set.
- audit.md:183-184. Doc: an unreachable syslog daemon fails boot. Code: only for `tcp` and `unix` (internal/audit/syslog_sink.go:115). Ran: `tcp` to a closed port exits 1, `udp` (the default) to a closed port boots.
- audit.md:208. Doc: ship copies "before local rotation removes them". Code: rotation never removes a file, Sluice has no audit retention (no remove call in internal/audit).
- compliance/gdpr.md (will be deleted, see last section). Doc: budget counters are kept for `retentionDays`. Code: old rows are deleted only at process start (internal/budget/manager.go:108, 296-298).

### Broken code blocks

- hardening.md:95. The `SubjectBinding` validates, but its `rateLimit` and `budget` are not enforced (no `claims.subjectId`, no `issuer`). Details above.

Not broken, but the writer should know:
- audit.md:92. `printf '%s' "$SEED" | shasum -a 256` gives the right anchor only when `$SEED` is byte-equal to the secret. The seed is used raw (internal/secrets/resolver.go:74-99, no trim). Ran: a `secret://file` seed with a trailing newline needs `sha256sum < file`, the trimmed value fails with exit 4. Same for the pepper: `sluice apikey hash --pepper secret://file/...` on a file with a trailing newline gives another hash than `--pepper <text>`.
- `sluice audit verify --help` says malformed JSON exits 1. Ran: an invalid JSON line exits 4 (internal/audit/verify.go:89). The docs page does not repeat the wrong value.

### Proposed pages for this section

1. `security/index.md`. What is on by default and what is not: default-deny, read-only, fail-closed audit, admin listener off by default and open without a token, plain HTTP by default. Link to the vulnerability report file.
2. `security/threat-model.md`. The one table (draft rows below), the diagram, a short list of limits.
3. `security/audit.md`. Record fields taken from a real record, the event types that are written, chain and genesis, file sink, what `failClosed` checks, `sluice audit verify` with flags, exit codes and limits, syslog and S3 sinks, pruning. Remove "Declaring sinks in YAML" (manifest is not wired), keep one warning line. Move the tail endpoint details to `reference/admin-api.md`, keep one line.
4. `security/hardening.md`. Checklist of Sluice keys only. Drop the Audit block (three duplicates of audit.md, link instead). Fix the binding example (`claims.subjectId`, `budget.enabled`).
5. `security/key-rotation.md`. Keep. Seven materials, one short procedure each. Fix HS secret, genesis and admin token rows.

No merge proposed. Hardening and key rotation are both checklists but answer different questions, and each stays under 350 words.

Draft rows for the new threat table. Columns: Threat, What Sluice does, code, What you must do.

| Threat | What Sluice does | Code | What you must do |
|---|---|---|---|
| Read a table no policy allows, also through JOIN, subquery, UNION | 403 `ACL_DENIED` (ran) | internal/policy/conflict.go:145-168 | Allow only the tables that may be read |
| Write (INSERT, UPDATE, DELETE, MERGE) | 403 `ACL_REJECTED`, or `ACL_DENIED` when no allow matches. Sources are attached `READ_ONLY` (ran) | internal/rewriter/rewriter.go:353-361, internal/datasource/drivers/sqlitefile/driver.go:106 | Give Sluice a read-only database user |
| DDL, COPY, BEGIN | 400 `ERR_UNSUPPORTED_SYNTAX`, or `ACL_DENIED` when no allow matches (ran) | internal/rewriter/rewriter.go:361-368 | Nothing |
| ATTACH, INSTALL, LOAD, PRAGMA, SET | Not parsed: 400 `ERR_SYNTAX`. SET is refused by DuckDB: `ERR_QUERY_FAILED` (ran) | internal/pgquery/parser.go:69, internal/executor/harden.go:57-82 | Nothing |
| Two statements in one request | 400 `ERR_MULTIPLE_STATEMENTS` (ran) | internal/pgquery/parser.go:76 | Nothing |
| Read around policy through a function (`query_table`, `read_csv`, `duckdb_settings`, `current_setting`) | Functions are allowlisted: 403 `ACL_REJECTED` (ran) | internal/parser/functions.go:16-49, internal/queryservice/execute.go:146-152 | Nothing |
| Read rows of another tenant | `RowFilterPolicy` wraps the table in a subquery with the predicate as bound parameter (ran) | internal/rewriter/inject_filter.go:404-448 | Write a filter for every tenant table. Keep the filter column in every matched table, else reads fail with `ERR_QUERY_FAILED` |
| Read a PII column | `ColumnMaskPolicy` masks the column inside the table wrapper (ran: `null`) | internal/rewriter/inject_filter.go:255-335, pkg/mask/ | Pick the mask type. `partial` shows a prefix, unsalted `sha256` can be guessed |
| See policy SQL or bound values with EXPLAIN | 400 `ERR_UNSUPPORTED_SYNTAX` when the table has a filter or mask (ran). EXPLAIN on other tables is served and shows the source file path | internal/rewriter/rewriter.go:180-183 | Nothing |
| Read bound values with `$n` | 400 `ERR_UNSUPPORTED_SYNTAX` (ran) | internal/queryservice/execute.go:155-162 | Nothing |
| Name over 63 bytes (parser cuts it, DuckDB does not) | 400 `ERR_UNSUPPORTED_SYNTAX` (ran) | internal/pgquery/parser.go:65, 148-170 | Nothing |
| SQL that DuckDB reads in another way than the parser | Parse check before the rewrite: 400 `ERR_UNSUPPORTED_SYNTAX`, or `ERR_SYNTAX` when DuckDB cannot parse it (ran both) | internal/rewriter/dialect.go:322-337 | Nothing |
| Two-part name `shop.customers` | 403 `ACL_REJECTED` (ran) | internal/queryservice/execute.go:168-175 | Write catalog.schema.table |
| Dump a table in one query | `QueryRewritePolicy` limit, sample, timeout. `limits.maxRows` (ran: limit) | internal/rewriter/apply_rewrite.go:18-46, internal/queryservice/execute.go:308-314 | Add a backstop policy on `tables: ["**"]` |
| Dump a table over many queries | Daily budget per subject: 429 `ERR_BUDGET_EXCEEDED`. Checked before the query, so the query that crosses the budget is served (ran) | internal/budget/manager.go:125-145, internal/queryservice/execute.go:73-83 | Set `budget.enabled: true`. Set `claims.subjectId` or `issuer` on the binding |
| Request flood | Global and per-IP buckets before login, per-subject bucket after: 429 `ERR_RATE_LIMITED` (ran per-subject) | internal/transport/rest/ratelimit.go:43-64, internal/ratelimit/limiter.go:123-141 | Set `rateLimit` plus `claims.subjectId` or `issuer`. `limits.perIpRps` is off by default |
| Risky query shape | `QueryRejectPolicy` CEL rule: 403 `ACL_REJECTED` (ran) | internal/policy/conflict.go:70-95 | Write the rules |
| Sensitive read needs a person | `ApprovalPolicy` holds the query: 202 `ERR_APPROVAL_PENDING`. Accept and reject URLs carry a token, unknown id and bad token give the same 404, a grant runs the identical query once (ran) | internal/queryservice/execute.go:251-279, internal/transport/rest/approvals.go:19-64, internal/approval/broker.go:238-262, 330 | Set `approval.publicBaseUrl` and a webhook. Keep the webhook channel private. Run one instance |
| Stolen API key | Only `HMAC-SHA256(pepper, id:material)` is stored, compare in constant time. Rate limit and budget cap the use | internal/identity/apikey.go:153-157, 219-232 | Keep the pepper secret. One key id per client. Rotate with two entries |
| Forged JWT | Algorithm allowlist without `none`, signature check, issuer binding, audience when set (ran: wrong HS secret gives 401) | internal/identity/jwt.go:39, 166-168, 224-228 | Set `issuer`, `audience`, `jwksUrl` |
| Edited audit log | Hash chain. `sluice audit verify` exits 4 with file and line (ran) | internal/audit/verify.go:49-131 | Run verify with `--anchor` on a schedule. Keep a copy and the last hash off the host: removed newest records are not detected |
| Query served without audit record | `audit.failClosed` (default true): 503 `ERR_AUDIT_UNAVAILABLE` when the queue does not take the record in 200 ms (read) | internal/queryservice/audit_helpers.go:212-225, internal/audit/dispatcher.go:206-230 | Keep it true. Alert on `sluice_audit_write_errors_total`: a failed disk write does not stop queries |
| Open admin API | Own listener, off by default, bearer token compared in constant time (ran) | internal/transport/admin/auth.go:16-31, internal/config/server.go:302-303 | Put `admin.token` in `sluice.yaml`. Bind to a private interface |
| Traffic read on the wire | TLS and mTLS on both listeners, TLS 1.2 minimum (ran) | internal/transport/tlsutil/tlsutil.go:30, 43 | Set `rest.tls` and `admin.tls` or use a TLS proxy. Default is plain HTTP |
| Secret file open to other users | Refuses group or world writable files, warns on world readable (ran) | internal/secrets/file.go:51-59 | `chmod 0600` |
| DuckDB reads local files or other URLs | External access off, extension install off, settings locked. Carve-out for the configured S3 bucket and HTTP URLs only (read) | internal/executor/harden.go:52-82 | List only the URLs you need |

Leave out of the table, no code: audit records for failed logins and reloads, an audit trail for DryRun policies, identity from client certificates.

### Features the code has that the section does not document

- `sluice audit verify --json` (cmd/sluice/audit.go:83). Ran: `{"files":1,"last_hash":"...","ok":true,"records":11}`, on failure `{"error":"...","file":"...","line":4,"ok":false}` on stderr. Documented only in reference/cli.md:215.
- `GET /admin/audit/tail`: default `n=100`, capped at 1000, NDJSON, `n=0` gives 400 (internal/transport/admin/audit.go:24-33). Ran.
- Audit record fields not on the page: top-level `remote_ip`, `message`, `extras` (keys seen: `rewrite`, `approval_id`, `approval_policies`, `sql_hash`, `origin` on genesis) (internal/audit/record.go:82-85).
- Verify limits: removed newest records pass (ran: exit 0), an empty directory passes (ran: `chain OK (0 file(s), 0 record(s), last_hash=)`).
- `admin.enabled` defaults to false (internal/config/server.go:302). No admin API and no `/metrics` until it is set.
- S3 sink keys `insecure`, `forcePathStyle`, `maxBufferBytes` (default 8 MiB) and `sessionToken` in the credentials JSON (internal/config/server.go:164-171, cmd/sluice/auditwire.go:124-128).
- Syslog facilities accepted: kern, user, daemon, auth, syslog, local0 to local7 (internal/audit/syslog_sink.go:60-64).
- `secret://` providers: `env` and `file` only. `vault`, `aws-sm`, `gcp-sm` are rejected at load (internal/secrets/urls.go:27-31). The example `server.yaml` comment names Vault.
- Approval URLs ignore HEAD and prefetch requests with 204 (internal/transport/rest/approvals.go:23-26, 102-112). Ran.
- `sluice config validate --strict` does not flag unknown server config keys. Ran: `audit.fle.path` passes, the server then falls back to the temp directory.
- DataSource manifests load from `policies.directory`. `datasources.directory` is parsed and never read (cmd/sluice/bootstrap.go:129-135).

### Links to root files

docs/security links to `SECURITY.md` (index.md:40-43, threat-model.md:113-114) and `THREAT_MODEL.md` (index.md:37-39, threat-model.md:5-8). No sentence is copied (checked by script). index.md restates two facts of `SECURITY.md`: `security@bino.bi` and the 72 hour acknowledgement (SECURITY.md:22, 36), values match.

## docs/security/index.md

Prose words now: 257. Suggested after rewrite: 120.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-6 | Sits between untrusted clients and data, "five properties" | fluff | The count goes stale |
| 2 | 8-10 | Default-deny. Empty policy directory is valid and rejects every query | implemented (internal/policy/conflict.go:145) | Ran: boots with an empty directory. `SELECT * FROM shop.main.admin_users` gives 403 `ACL_DENIED`. A request without credentials gets 401 first |
| 3 | 11-12 | Only SELECT, EXPLAIN, SET, SHOW, PRAGMA reach the engine | mismatch | PRAGMA never parses: 400 `ERR_SYNTAX`. SET is refused by DuckDB. See summary |
| 4 | 12 | Writes are rejected before execution | implemented (internal/rewriter/rewriter.go:353) | Ran: INSERT, UPDATE, DELETE give 403 `ACL_REJECTED` |
| 5 | 12-13 | Every data source is attached `READ_ONLY` | implemented (internal/datasource/drivers/sqlitefile/driver.go:106) | All drivers force it. `readonly: false` is ignored (sqlitefile/driver.go:59-63, duckdbfile/driver.go:52-55) |
| 6 | 13-15 | EXPLAIN refused on a table with a row filter or mask | implemented (internal/rewriter/rewriter.go:180) | Ran: 400 `ERR_UNSUPPORTED_SYNTAX` |
| 7 | 16-17 | Query not served unless the access record is "durably enqueued" | mismatch | Enqueued in memory, written to disk up to 200 ms later. See summary |
| 8 | 18-20 | `/admin/*` and `/metrics` on their own listener `:9091` behind a static bearer token | implemented (internal/transport/admin/routes.go:18) | Ran: 401 without token. Listener is off by default. Empty token means open |
| 9 | 21 | Credentials are `secret://` references, never inlined | mismatch | `admin.token` is an inline string (internal/config/server.go:70) |
| 10 | 22 | Secret values never appear in logs, metrics or audit records | implemented (pkg/mask/hash.go:22) | Ran: grep for 10 secret values (pepper, seeds, HS secrets, hashes, admin token, key material) in server logs, audit files, syslog output and `/metrics`: 0 hits. `sql_sample` and `client_meta` store caller text as sent |
| 11 | 26-36 | Chapter table, link to architecture/security-model.md | fluff | Navigation |
| 12 | 37-39 | Full STRIDE analysis in root `THREAT_MODEL.md` | duplicate | Same link on threat-model.md:5. Root file is not part of the docs |
| 13 | 40-43 | Report through `SECURITY.md`, `security@bino.bi`, 72 hour acknowledgement | duplicate | Owner: root `SECURITY.md` (lines 22, 36). Values match |

### Code blocks

None.

## docs/security/audit.md

Prose words now: 1073. Suggested after rewrite: 550.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-8 | Every terminal outcome appends at least one record | mismatch | No record for 401, transport 429, `service saturated`, admin calls except explain. Deny, reject, error and allow paths do write (internal/queryservice/execute.go:216-231, audit_helpers.go:196, 289-318). Ran |
| 2 | 8-10 | The chain makes tampering detectable | implemented (internal/audit/verify.go:49) | Ran: edited field and deleted line both exit 4. Removed newest records are not detected |
| 3 | 16 | `timestamp`, `event_type`, `query_id`. Id echoed as `X-Query-Id` | implemented (internal/audit/record.go:63) | Ran: header present (internal/transport/rest/query.go:65) |
| 4 | 17 | `subject`: id, method (jwt, api_key, admin_token, none), issuer, email, groups, remote_ip | mismatch | Only `jwt` and `api_key` are set. `remote_ip` is `host:port`. See summary |
| 5 | 18 | `origin`: rest, mcp, admin | implemented (internal/queryservice/service.go:190) | Approval lifecycle records put the hostname there (cmd/sluice/approval.go:135). Ran |
| 6 | 19 | `sql_fingerprint`, `rewritten_fingerprint`, `sql_sample`. `audit.sqlSampleBytes` default 2048, 0 disables | implemented (internal/queryservice/audit_helpers.go:24) | Default at internal/config/server.go:335 |
| 7 | 19 | `rewritten_fingerprint` changes when masked columns change at the source | internal detail | |
| 8 | 20 | `tables`, `catalogs` | implemented (internal/audit/record.go:71) | Ran |
| 9 | 21 | `policies_applied`, `decision` (allow, deny, reject, error), `error_code` | implemented (internal/audit/record.go:39) | Ran: a write refused by the statement gate is `decision: error` with `error_code: ACL_REJECTED`. Default-deny is `decision: deny` with no `error_code` |
| 10 | 22 | `row_count`, `truncated`, `duration_ms` only on `query-result` | mismatch | `duration_ms` is on every query record. `row_count` and `truncated` are left out when 0 or false |
| 11 | 23 | `client_meta` from request `meta`, capped at 16 keys, 64 byte keys, 256 byte values | implemented (internal/queryservice/audit_helpers.go:49) | Ran: `"client_meta":{"purpose":"audit-test","ticket":"T-1"}` |
| 12 | 24 | `sluice_version`, `duckdb_version`, `parser_version` | mismatch | `duckdb_version` is never set. `parser_version` on genesis only |
| 13 | 25 | `prior_hash`, `hash` | implemented (internal/audit/record.go:91) | |
| 14 | 27-30 | Event types `query`, `query-result`, `genesis`, `admin-action`, four `approval-*` | implemented (internal/queryservice/audit_helpers.go:32) | Ran: saw query, query-result, genesis, admin-action, approval-requested, approval-approved. `admin-action` is written for policy explain only (internal/queryservice/tables.go:56) |
| 15 | 27-28 | Event types `policy-decision`, `config-reload`, `auth-event`, `datasource-connect` | not implemented | No emitter |
| 16 | 34-36 | `hash = sha256(prior_hash + "\n" + canonical JSON without hash)` | implemented (internal/audit/hashchain.go:18) | Test: TestComputeHash_StableAndChains |
| 17 | 36-37 | First record anchored on `sha256(seed)`, seed from `audit.file.genesis` | implemented (internal/audit/dispatcher.go:110) | Ran: anchor `421caf3e...` equals `sha256("hello-sluice-demo-genesis")`. Seed bytes are used raw. Seed is read only for an empty directory |
| 18 | 38 | Changing a field breaks every hash after it | implemented (internal/audit/verify.go:109) | Ran. Verify stops at the first bad line |
| 19 | 38-40 | Key order and whitespace changes are tolerated | implemented (internal/audit/verify.go:86) | Ran: reordered keys plus spaces on one line, `chain OK` |
| 20 | 53-56 | Without `genesis` the seed is hostname plus build commit | implemented (cmd/sluice/bootstrap.go:598) | |
| 21 | 56-57 | Always set a stable seed, `openssl rand -hex 32` | fluff | Generic. Keep one sentence |
| 22 | 61-62 | Files `audit-YYYY-MM-DD.jsonl`, directory `0750`, files `0640` | implemented (internal/audit/file_sink.go:120) | Ran: `stat` shows 750 and 640 |
| 23 | 62-63 | Rotation at midnight UTC and at `rotateSizeMB` (`-1`, `-2`) | implemented (internal/audit/file_sink.go:252) | Read. Tests: TestFileSink_AppendAndRotateDaily, TestFileSink_SizeRotation. `rotateDaily: false` with no size still rotates daily (file_sink.go:162) |
| 24 | 63-64 | On restart the chain continues from the newest file | implemented (internal/audit/file_sink.go:276) | Ran: restart, `chain OK` over old and new records |
| 25 | 66-69 | No `audit.file.path`: writes to `$TMPDIR/sluice-audit` with a warning | implemented (cmd/sluice/auditwire.go:45) | Ran |
| 26 | 73-76 | `audit.failClosed: true` is the default. Enqueue failure returns `ERR_AUDIT_UNAVAILABLE` and no data | implemented (internal/queryservice/audit_helpers.go:212) | Read. HTTP 503 (pkg/errors/codes.go:81). "Durably" is wrong, see index row 7 |
| 27 | 78-80 | `failClosed: false`: queries flow, drops counted in `sluice_audit_dropped_total` | implemented (internal/queryservice/audit_helpers.go:220) | Read. Counter at internal/audit/dispatcher.go:233 |
| 28 | 81-82 | An unaudited query is worse than a failed one | fluff | |
| 29 | 84-85 | Queue of 10,000 records, 200 ms enqueue deadline | implemented (internal/audit/dispatcher.go:74) | Not configurable (cmd/sluice/bootstrap.go:241-246) |
| 30 | 85-86 | A stuck disk gives fast visible failures | fluff | Only a blocked write fills the queue. A write that returns an error is counted and the query is served (dispatcher.go:267-273) |
| 31 | 86 | Shutdown drains the queue for up to 10 s | implemented (internal/audit/dispatcher.go:85) | Read |
| 32 | 87-88 | Alert on three audit metrics | duplicate | Owner: reference/metrics.md. Metrics exist (internal/audit/metrics.go:34-36) |
| 33 | 97-98 | Verify walks every `.jsonl` file in date order across file borders | implemented (internal/audit/verify.go:49) | Test: TestVerify_HappyPathAcrossRotations |
| 34 | 98-100 | `--anchor` pins the first `prior_hash` | implemented (cmd/sluice/audit.go:82) | Ran: wrong anchor exits 4 |
| 35 | 100-101 | Exit codes 0, 1, 4 with file and line | implemented (cmd/sluice/audit.go:47) | Ran: 0, 1 (missing directory), 4 |
| 36 | 103-106 | What to do when verify fails | fluff | Generic incident advice |
| 37 | 110-115 | Tail through the admin API | duplicate | Owner: reference/admin-api.md. Works (internal/transport/admin/audit.go:17) |
| 38 | 127-128 | An `AuditSink` manifest records sink intent | not implemented | Never read by serve |
| 39 | 142-147 | Manifests with `s3` or `syslog` fail validation, exit 3, serve refuses | implemented (pkg/apitypes/decoder.go:515) | Ran: exit 3 from `policy validate` and `config validate`, serve exits 1 |
| 40 | 143-146 | `postgres` and `otlp` sinks | not implemented | Page says so |
| 41 | 151-172 | Syslog and S3 sinks from `audit.*` receive every record | implemented (cmd/sluice/auditwire.go:56) | Ran syslog over UDP: 27 records, 27 messages. S3 not run (no bucket), boots with a warning when the endpoint is down |
| 42 | 176-178 | File sink is the record of truth. Outage of a secondary sink never blocks a query | implemented (internal/audit/syslog_sink.go:145) | Test: TestDispatcher_SecondarySinkFailure_FileChainStillVerifies. The gate checks the queue, not the file write |
| 43 | 179-181 | Syslog: RFC 5424, octet counting on streams, one reconnect, failures counted | implemented (internal/audit/syslog_sink.go:186) | Ran: `<134>1 2026-10-12T14:54:24.787702336Z host sluice 558728 - - {...}`. Metric reason is `write_error` |
| 44 | 182-183 | UDP can lose messages, TCP syslog has no TLS | implemented (internal/audit/syslog_sink.go:115) | Plain `net.Dial` |
| 45 | 183-184 | An unreachable daemon fails boot | mismatch | True for tcp and unix only |
| 46 | 185-191 | S3: NDJSON objects `<prefix>/YYYY/MM/DD/audit-<ulid>.jsonl`, size or interval trigger, retry from memory, drop past `maxBufferBytes` | implemented (internal/audit/s3_sink.go:154) | Read. Tests in s3_sink_test.go. A crash loses the whole buffer, up to `maxBufferBytes` |
| 47 | 192-195 | Object Lock mode and retention per object. Bucket must be created with lock | implemented (internal/audit/s3_sink.go:222) | Read. `retentionDays` required with `objectLock` (ran: exit 3 without) |
| 48 | 195-197 | No `credentialsRef`: env, shared file, IAM chain | implemented (cmd/sluice/auditwire.go:136) | Read |
| 49 | 201-207 | Prune whole files, oldest first. The rest verifies without `--anchor` | implemented (internal/audit/verify.go:91) | Ran: first record removed, OK without anchor, exit 4 with anchor |
| 50 | 208 | Copy files away before local rotation removes them | mismatch | Rotation never removes files |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 42 | yaml | `audit` block with `failClosed`, file sink, genesis | untested, verified by hand | `sluice config validate --strict --config` exit 0. Same keys ran in the example server |
| 92 | console | `sluice audit verify <dir> --anchor ...` with output | untested, verified by hand | Real output: `chain OK (1 file(s), 11 record(s), last_hash=efac312e7a61a09a…)`. Format matches. Anchor caveat in summary |
| 112 | bash | `curl` of `/admin/audit/tail?n=50` | untested, verified by hand | Ran on port 18201: 200, NDJSON |
| 119 | bash | `jq` filter over audit files | untested, not verified | No `jq` on this machine. Field names `tables`, `timestamp`, `subject.id`, `decision`, `error_code` match a real record |
| 130 | yaml | `AuditSink` manifest, type file | untested, verified by hand | `sluice policy validate --strict` exit 0. Has no effect at run time |
| 153 | yaml | `audit` block with syslog and s3 | untested, verified by hand | `config validate` exit 0. Syslog ran with `network: udp`. S3 part not run |

## docs/security/threat-model.md

Prose words now: 742. Suggested after rewrite: 400.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-10 | STRIDE analysis in root `THREAT_MODEL.md`, changed by pull request and RFC | fluff | Link to a root file plus process text |
| 2 | 12-21 | Assets table | fluff | Admin token row: the token also opens audit tail, metrics, approvals and datasource status (internal/transport/admin/routes.go:18-26) |
| 3 | 23-32 | Four adversaries | fluff | |
| 4 | 62 | Table nobody allowed: `ACL_DENIED` | implemented (internal/policy/conflict.go:145) | Ran: 403. Also for a denied table in a JOIN, UNION or subquery: `no SqlAccessPolicy allows shop.main.admin_users` |
| 5 | 63 | INSERT, UPDATE, DELETE: `ACL_REJECTED` | implemented (internal/rewriter/rewriter.go:359) | Ran: 403 `write operations are not permitted`. `ACL_DENIED` comes first when no allow matches |
| 6 | 64 | DDL, COPY, ATTACH: `ERR_UNSUPPORTED_SYNTAX` | mismatch | ATTACH gives `ERR_SYNTAX`. DDL without an allowed table gives `ACL_DENIED`. See summary |
| 7 | 65 | Functions `query_table`, `read_csv`, `duckdb_settings`, `current_setting`: `ACL_REJECTED` | implemented (internal/parser/functions.go:16) | Ran all four: 403, for example `table function "read_csv" is not allowed` |
| 8 | 66 | Cross-tenant rows: `RowFilterPolicy` with bound `$N` | implemented (internal/rewriter/inject_filter.go:404) | Ran: `WHERE tenant_id = 'widget'` returns 0 rows. Log shows `WHERE sluice_src.tenant_id = $1` |
| 9 | 67 | PII columns: `ColumnMaskPolicy` | implemented (internal/rewriter/inject_filter.go:302) | Ran: `email` comes back null |
| 10 | 68 | EXPLAIN refused with a filter or mask | implemented (internal/rewriter/rewriter.go:180) | Ran: 400 `ERR_UNSUPPORTED_SYNTAX` |
| 11 | 69 | Identifier over 63 bytes | implemented (internal/pgquery/parser.go:148) | Ran: 400 `ERR_UNSUPPORTED_SYNTAX` |
| 12 | 70 | `$n` parameters refused | implemented (internal/queryservice/execute.go:155) | Ran: 400 `query parameters ($n) are not supported` |
| 13 | 71 | DuckDB parse check: `ERR_UNSUPPORTED_SYNTAX`, `ERR_SYNTAX` when DuckDB cannot parse | implemented (internal/rewriter/dialect.go:322) | Ran: `SELECT (id > 1 OR id < 5) IS NULL ...` gives the first, `ORDER BY id USING <` the second |
| 14 | 72 | LIMIT clamp and sampling | implemented (internal/rewriter/apply_rewrite.go:18) | Ran limit: 2 rows, `truncated: true`. Sampling read only (apply_rewrite.go:55) |
| 15 | 73 | Daily budgets: `ERR_BUDGET_EXCEEDED` | implemented (internal/budget/manager.go:125) | Ran: 429. Needs `budget.enabled: true` and `claims.subjectId` or `issuer` |
| 16 | 74 | Per-subject rate limit: `ERR_RATE_LIMITED` | implemented (internal/ratelimit/limiter.go:123) | Ran: 429 after burst 2 |
| 17 | 75 | `QueryRejectPolicy` CEL rules: `ACL_REJECTED` | implemented (internal/policy/conflict.go:70) | Ran: `SELECT *` rule, 403 with `"policy":"no-star"` |
| 18 | 76 | `ApprovalPolicy` with single-use capability URLs, uniform 404 | mismatch | URL is replayable, the grant is single-use. 404 part is right (internal/transport/rest/approvals.go:41). Ran the full flow |
| 19 | 77 | Stolen key: peppered HMAC hashes, no key material at rest | implemented (internal/identity/apikey.go:153) | Keys that share one `claims.subjectId` look the same in the audit log (apikey.go:160-163) |
| 20 | 78 | Tampering detected at the exact line | implemented (internal/audit/verify.go:109) | Ran: `audit-2026-10-12.jsonl:4: hash mismatch: stored=... recomputed=...` |
| 21 | 80-83 | Global and per-IP buckets before login, per-subject buckets, `limits.defaultSubjectRps` | duplicate | Owner: policies/subjects.md. Code: internal/transport/rest/ratelimit.go:43, cmd/sluice/bootstrap.go:265 |
| 22 | 87-92 | What a mask output still shows | duplicate | Owner: policies/column-masks.md. Ran: `WHERE email = 'alice@acme.test'` under the null mask returns 0 rows |
| 23 | 93-97 | Missing filter column lets a crafted query read unfiltered rows | mismatch | Not reproduced, see summary. True part: no load-time check |
| 24 | 98-101 | A hijacked agent can still leak what it may read | fluff | Limit by design, no code |
| 25 | 102-104 | Aggregate inference | fluff | Limit by design, no code |
| 26 | 108-111 | Non-goals | fluff | One table cell is enough: direct database credentials bypass Sluice |
| 27 | 113-114 | Report through `SECURITY.md` | duplicate | Same as index.md:40 |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 36 | mermaid | Trust boundary diagram | diagram | No label is wrong. `:9091` is the default (internal/config/server.go:303). The `TLS` edges are optional: both listeners serve plain HTTP unless `tls` is set (internal/transport/rest/server.go:165) |

## docs/security/hardening.md

Prose words now: 533. Suggested after rewrite: 300.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-7 | Most items map to a config field | fluff | |
| 2 | 11-13 | `Dockerfile` builds a distroless image as `nonroot:nonroot`, uid 65532 | implemented (Dockerfile:113) | Base image at Dockerfile:96. Same in Dockerfile.release:16, 27 |
| 3 | 14-15 | Mount policy and datasource directories read-only | fluff | Generic container advice. There is one directory: `datasources.directory` is never read |
| 4 | 16-18 | `secret://file` refuses group or world writable files, warns on world readable | implemented (internal/secrets/file.go:51) | Ran: mode 0664 gives `refusing to read secret`, mode 0644 gives a WARN line |
| 5 | 19-20 | Audit directory on its own volume, `0750` and `0640` | duplicate | Owner: audit.md |
| 6 | 24-26 | TLS with `rest.tls.certFile` and `keyFile`. Warning on plain HTTP | implemented (internal/transport/rest/server.go:165) | Ran: `rest: serving plain HTTP (no TLS configured)` |
| 7 | 27-29 | `admin.listen` default `:9091`, carries `/admin/*` and `/metrics` | implemented (internal/config/server.go:303) | `admin.enabled` defaults to false |
| 8 | 30-31 | Empty token boots with a warning and an open admin API | implemented (internal/transport/admin/server.go:109) | Ran |
| 9 | 35-39 | `clientCA` requires a client certificate, TLS 1.2 floor. Same block under `admin.tls` | implemented (internal/transport/tlsutil/tlsutil.go:43) | Ran with scratch certificates: no client cert fails the handshake, TLS 1.1 fails, admin HTTPS works |
| 10 | 53-54 | A `tls` block needs both files. Unreadable files abort startup | implemented (internal/config/validate.go:127) | Ran: `config validate` exit 3, serve exit 1. Missing files: serve exit 2 |
| 11 | 54-57 | mTLS gates the transport only, requests still need JWT or API key | implemented (internal/transport/rest/routes.go:35) | Ran: valid client cert, no key, 401 |
| 12 | 57 | Certificate identity is on the roadmap | not implemented | |
| 13 | 61-62 | `identity.apiKeyPepper` must be a `secret://` reference. Without it API keys are off | implemented (cmd/sluice/bootstrap.go:645) | Ran: a literal value fails `config validate` with exit 3 |
| 14 | 63-64 | Pass the admin token as `SLUICE_ADMIN__TOKEN` instead of writing it into the file | mismatch | Ignored when the YAML has no `admin.token` key. See summary |
| 15 | 65-66 | Pin `issuer`, `audience` and `jwksUrl` on JWT bindings | implemented (internal/identity/jwt.go:224) | Audience is checked only when set |
| 16 | 67-69 | Read-only database credentials through `credentialsRef`. Sources are attached `READ_ONLY` | implemented (internal/datasource/drivers/postgres/driver.go:163) | Read |
| 17 | 70-74 | `s3` `allowedPaths` and `http` URLs are the DuckDB external access carve-out | mismatch | S3 carve-out is the whole bucket |
| 18 | 75-77 | `headersRef` headers are scoped per origin, every origin gets the same set | implemented (internal/datasource/drivers/httpfile/driver.go:233) | Read |
| 19 | 78 | Plan rotation up front | fluff | |
| 20 | 82-83 | Keep `audit.failClosed: true` | duplicate | Owner: audit.md |
| 21 | 84-85 | Pin `audit.file.genesis` | duplicate | Owner: audit.md |
| 22 | 86-88 | Run verify on a schedule, alert on two metrics | duplicate | Owner: audit.md |
| 23 | 92-93 | Rate limit and budget on every agent binding | mismatch | The example enforces neither. See summary |
| 24 | 113-114 | Global `QueryRewritePolicy` backstop | implemented (internal/rewriter/apply_rewrite.go:18) | Ran with `max: 1` on `tables: ["**"]`: every table returns 1 row |
| 25 | 133-134 | Check default-deny in CI with a policy test | implemented (internal/policytest/policytest.go:274) | Ran: `PASS  unknown subject is denied` |
| 26 | 148-151 | DryRun first, watch the audit log for would-be denials | mismatch | The audit log has no trace of DryRun matches |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 41 | yaml | `rest.tls` with `clientCA`, `admin.tls` | untested, verified by hand | `config validate --strict` exit 0. Ran the same shape with scratch certificates. Feature tests: internal/transport/rest/tls_test.go, internal/transport/admin/tls_test.go |
| 95 | yaml | `SubjectBinding` with `rateLimit` and `budget` | broken | Validates, limits are not enforced. Needs `claims.subjectId` or `issuer`, and `budget.enabled: true` |
| 116 | yaml | `QueryRewritePolicy` global backstop | untested, verified by hand | `policy validate --strict` exit 0. Behavior ran with a lower `max` |
| 136 | yaml | Policy test for default-deny | untested, verified by hand | `sluice policy test` on the hello-sluice policies: 1 passed, exit 0 |

## docs/security/key-rotation.md

Prose words now: 785. Suggested after rewrite: 350.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-6 | Everything long-lived can be rotated | fluff | |
| 2 | 10 | Pepper: `identity.apiKeyPepper`, restart, breaks every hash | implemented (cmd/sluice/bootstrap.go:645) | Read at startup only, no setter (internal/identity/apikey.go:154) |
| 3 | 11 | API key: `apiKeys[].hashRef`, policy reload, that key only | implemented (cmd/sluice/bootstrap.go:458) | Ran |
| 4 | 12 | RS and ES keys follow the issuer's JWKS | implemented (internal/identity/jwks.go:97) | Read |
| 5 | 13, 75-78 | HS secret: restart, resolved once at startup, hard cutover | mismatch | Re-resolved on every policy reload. Ran |
| 6 | 14, 82, 86-88 | Genesis seed: restart starts a new chain, a mixed directory will not verify | mismatch | The seed is used only for an empty directory. Ran |
| 7 | 15 | Mask salt and FPE key: reload for a new reference, up to 10 min cache in place | implemented (internal/secrets/resolver.go:43) | Read. Any policy reload clears the cache (cmd/sluice/bootstrap.go:449) |
| 8 | 16 | Admin token: `admin.token` or `SLUICE_ADMIN__TOKEN`, restart | mismatch | The env var alone does not set the token. YAML key plus restart is right (cmd/sluice/bootstrap.go:418-422) |
| 9 | 20-22 | Hash is `HMAC-SHA256(pepper, id + ":" + material)`. Pepper is not hot-reloaded | implemented (internal/identity/apikey.go:219) | Ran: `sluice apikey hash --pepper hello-sluice-demo-pepper --id sl_demo_hello --material world` prints `c0c9ab61...5153`, the value in the example compose file |
| 10 | 24-30 | Pepper rotation steps with `sluice apikey hash --pepper <ref> --id ... --material ...` | implemented (cmd/sluice/apikey.go:35) | Ran with a raw value, `secret://env/...` and `secret://file/...`: same hash. Missing flag: exit 1. Pepper bytes are used raw, a trailing newline in the file changes the hash |
| 11 | 32-34 | One instance accepts one pepper. Hard cutover, or a second instance | implemented (internal/identity/apikey.go:154) | Read |
| 12 | 38-39 | `hashRef` is resolved again on every reload, secret cache cleared first | implemented (cmd/sluice/bootstrap.go:449) | Ran: overwrote the hash file, `POST /admin/reload`, old key 401, new key 200 |
| 13 | 39-50 | Overlap with two key entries | implemented (internal/identity/apikey.go:113) | Ran: both keys 200. One hash per `id`, so the overlap needs two ids |
| 14 | 52-57 | Key format `<id>.<material>`. `subject.id` is the key id when `claims.subjectId` is not set | implemented (internal/identity/apikey.go:160) | Ran: audit record shows `"id":"report-bot-2026b","issuer":"dual"`. Split on the last dot (apikey.go:208) |
| 15 | 61-68 | JWKS cached per URL for `jwksCacheTtl` (default 10 min). Unknown `kid` refreshes at once, at most one per 30 s | implemented (internal/identity/jwks.go:69) | Read. Refresh rule at jwks.go:110-125 |
| 16 | 70-71 | Lower `jwksCacheTtl` for faster revocation | implemented (internal/identity/jwt.go:257) | Read. When the JWKS fetch fails, the old keys keep working (jwks.go:128-141) |
| 17 | 75 | One HS secret per issuer | implemented (cmd/sluice/bootstrap.go:720) | Trailing newline of the secret is trimmed here, unlike pepper and seed |
| 18 | 84-95 | Seed rotation with a fresh directory, verify each part with its own anchor | implemented (internal/audit/dispatcher.go:104) | Ran the pieces: a fresh directory gets a genesis record from the configured seed |
| 19 | 99-101 | `hash` with `hmac_sha256` takes `saltRef`, `fpe` takes `keyRef`, both deterministic | implemented (pkg/mask/hash.go:55) | Read. `fpe` at pkg/mask/fpe.go:46. Plain `sha256` also takes an optional `saltRef` (hash.go:29) |
| 20 | 103-107 | After rotation the same input masks to another value | implemented (pkg/mask/hash.go:35) | Read. Follows from the salt being part of the digest |
| 21 | 109-113 | No restart. New reference resolves on reload. Same reference: old value for up to 10 min | implemented (internal/secrets/resolver.go:75) | Read |
| 22 | 117-120 | Token compared in constant time. `/metrics` uses the same token | implemented (internal/transport/admin/auth.go:23) | Ran: `/metrics` without token gives 401 |
| 23 | 124-127 | What rotation does not invalidate | fluff | Repeats the audit page |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 41 | yaml | Fragment with two `apiKeys` entries | untested, verified by hand | Ran inside a full `SubjectBinding`: both keys 200, after removing one entry and reload the old key gets 401 |
| 92 | console | Two `sluice audit verify` calls with old and new anchor | untested, verified by hand | Ran the command form on two scratch directories. No output is shown in the docs |

## Compliance pages: unique implemented facts

Read once: docs/compliance/index.md, gdpr.md, bsi-c5.md, soc2.md. Mapping tables and legal text ignored. Checked with `grep -rn` over `docs/` without `docs/compliance/`.

| Fact | Source | Code | Proof it is not elsewhere | Move to |
|---|---|---|---|---|
| Policy explain calls are written to the audit log as `admin-action`. Reloads and all other admin endpoints write no audit record | bsi-c5.md (IDM-09 row) | internal/queryservice/tables.go:55-71 (explain, used by `GET /admin/subjects/explain` and MCP `explain_access`). internal/transport/admin/reload.go:20 has no audit call. Ran: explain wrote `{"event_type":"admin-action","message":"policy explain","origin":"admin",...}`, 5 reloads wrote nothing | `grep -rn "admin-action" docs/` hits only security/audit.md:28 (the bare name). `grep -rniE "explain.*audit\|not (currently )?audited"`: 0 hits | security/audit.md, event types |
| The budget store keeps one row per UTC day, subject id and issuer with CPU milliseconds and rows served. Rows older than `budget.retentionDays` (default 35) are deleted | gdpr.md (Data subject rights) | internal/budget/manager.go:77-78, 84-87, 296-298 | `grep -rn retentionDays docs/` shows the key and default only (policies/subjects.md:93, operations/server-config.md:104, reference/configuration.md:100). `grep -rniE "rows_served\|cpu_ms\|usage counter"`: only operations/index.md:18 "SQLite usage counters" | policies/subjects.md, Budgets |

Correction for the second fact: the delete runs once at process start (internal/budget/manager.go:108), not daily. A server that is never restarted keeps all rows.

Checked and already documented elsewhere, nothing to move:
- `sluice audit verify --json`: reference/cli.md:215.
- JWT algorithm allowlist, `alg: none` refused: policies/subjects.md:13-14.
- `approval.persist`, one instance only: policies/approvals.md:72-81.
- Driver-level spans not there yet: operations/observability.md:142.
- cosign, SBOM, SLSA provenance: getting-started/install.md:42.
- Validate-then-swap reload: operations/hot-reload.md:43.
- In-query and post-query mask split: docs/index.md:53.

Not product behavior, not carried over: CI scanners (`govulncheck`, `gosec`, `trivy`, CodeQL, 0 hits elsewhere), "only tag so far is v0.1.0-rc.2", no DSR tooling, no built-in reporting.
