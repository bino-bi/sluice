# Audit: Cookbook

Binary: `/tmp/sluice-bin/sluice` (commit 6f7b99f, default build, no `duckdb_use_static_lib` tag). Date: 2026-10-12.
All local runs used scratch copies under `/tmp/audit-cookbook/` and ports 18240 to 18258. "Verified" means I ran it. "Read" means code only.

## Summary

### Not implemented
- `examples/mcp-agent/server.yaml:41-45` (comment): `SLUICE_MCP_GROUPS` env var that gives an anonymous stdio session its groups. Not in the code (`grep -r SLUICE_MCP_GROUPS` finds only this comment). The real switches are `mcp.apiKeyRef`, `mcp.tokenRef`, `mcp.allowAnonymous` (`internal/config/server.go:51-63`).
- `examples/hello-sluice/server.yaml:37-38` (comment): audit genesis from Vault, AWS Secrets Manager or GCP Secret Manager. Only `env` and `file` providers exist, the other names are reserved and rejected (`internal/secrets/urls.go:17`, `internal/secrets/resolver.go:60-61`).
- `docs/cookbook/mcp-agents.md:124-126`: schema descriptions from a metadata store. The page itself says it is not implemented. Remove the admonition.
- `examples/break-glass/policies.d/bindings.yaml:36-39` and `mask-email.yaml:4-5` (comments): "an additional audit record" or "a separate audit record" tags elevated queries. No such record exists. The normal `query` record carries `subject.groups`.
- No CLI command and no admin endpoint can accept an approval. `sluice --help` has no approval command, `cmd/sluice/approval.go` is wiring only, `GET /admin/approvals` lists pending requests without tokens (`internal/transport/admin/approvals.go:12-27`).

### Docs and code disagree
- `index.md:5-6`: "each recipe maps 1:1 to a runnable directory". `examples/approval-workflow/` has no `server.yaml`, no data, no binding. `examples/mcp-agent/` does not start as shipped (see below).
- `index.md:6-7`: "the YAML on the page is the configuration you run". Drift in multi-tenant, break-glass, mcp-agents (details per page).
- `multi-tenant.md:57,65` and curl at `:91`: key ids `acme` and `beta`. Example: `sl_demo_acme`, `sl_demo_beta` (`examples/multi-tenant/policies.d/bindings-tenants.yaml:20,34`). `acme.supersecret` gives 401.
- `multi-tenant.md:76-78`: compose hashes "are placeholders, replace them". They are the real HMACs for `sl_demo_acme:supersecret` and `sl_demo_beta:supersecret` (`sluice apikey hash` gives the same values as `docker-compose.yaml:19-20`).
- `break-glass.md:44-65`: binding names `analyst`, `sre-on-call`, key ids `analyst`, `sre`. Example: `break-glass-analyst`, `break-glass-sre`, `sl_demo_analyst`, `sl_demo_sre` (`examples/break-glass/policies.d/bindings.yaml:15,20,28,33`). Page keys give 401.
- `break-glass.md:97-98` (and example README:78-79): "every record carries the caller's subject and groups". Only the `query` record does. The `query-result` record has `"subject":{}` (verified).
- Break-glass audit: `policies_applied` of the SRE query still lists `ColumnMaskPolicy/mask-email` although the exclude lifted the mask (`internal/policy/conflict.go:41-47` adds every matched policy, the exclude is applied later at `:284`). The only signals are `subject.groups` and `rewritten_fingerprint == sql_fingerprint`.
- `cross-source-joins.md:90,99`: key `analyst.supersecret`. Example: `sl_demo_analyst.supersecret` (`examples/cross-source-join/policies.d/binding-apikey.yaml:14`).
- `cross-source-joins.md:148`: "all six drivers". Code has seven types: postgres, mysql, sqlite, s3, http, duckdb_file, motherduck (`pkg/apitypes/datasource.go:58-64`).
- `mcp-agents.md:16-34`: config uses `sluice mcp ... --api-key=mcp.supersecret`. Example `claude-desktop.json:7` uses `serve` and no credential. Both fail with the example `server.yaml`: `mcp.enabled with transport=stdio requires a pinned credential` (`internal/config/validate.go:102`), exit 1.
- `mcp-agents.md:65-95`: page shows `QueryRewritePolicy/agent-guardrails` and `rateLimit` plus `budget` on the binding. The example directory has neither (`examples/mcp-agent/policies.d/` holds 3 objects).
- `mcp-agents.md:89-91` and `:44-45`: "per-day spend caps". The binding `budget:` block does nothing unless `budget.enabled: true` is set in `server.yaml` (`cmd/sluice/budgetwire.go:20`, default false). The example config does not set it. Verified: 3 queries of 5 rows pass a `rowsPerDay: 3` budget without the switch, `ERR_BUDGET_EXCEEDED` with it.
- `mcp-agents.md:110-111` and `:133-135`: `list_accessible_tables` returns `shop.main.products`, "an empty list is a policy gap". On a fresh process `list_accessible_tables` and `list_tables` return `[]`. They walk the schema cache (`internal/queryservice/tables.go:174`), which is filled lazily and is never warmed at start (`Refresh` has no caller outside tests). After one `describe_table` or query on the catalog both return `["shop.main.products"]`. Verified.
- `mcp-agents.md:121-122` (and example README:85): "every call lands in the audit log". Only `execute_sql` (`query`) and `explain_access` (`admin-action`, message `policy explain`) are audited. `whoami`, `list_catalogs`, `list_tables`, `list_accessible_tables`, `describe_table`, `check_approval` write no record. Verified.
- `approval-workflow.md:40-41`: "touching `ssn` or any `salary*` column triggers the approval". `SELECT * FROM shop.main.people` returns `ssn` and `salary_eur` with HTTP 200 and no approval. `columnsAccessed` only sees columns written in the SQL (`internal/policy/approval.go:55-60`, `internal/parser/shape.go:56-62`). Verified. The predicate trigger also misses `country IN ('de')` and `lower(country) = 'de'`.
- `approval-workflow.md:95-96`: "a rejected query returns `ERR_APPROVAL_REJECTED` (403), an expired request returns `ERR_APPROVAL_EXPIRED` (410)". True only for the request that is still waiting inside `syncWait` (`internal/queryservice/approval.go:91-102`). A re-run after a reject opens a new pending request (202). Verified both ways.
- `approval-workflow.md:76`: webhook `subject` shown as `{"id","issuer":"","email":"","groups"}`. Real payload omits empty `email` and, for an API key, `issuer` is the binding name (`internal/approval/request.go:20-25`).
- `pii-masking.md:177-178`: "`sha256` is unkeyed". `sha256` takes an optional `saltRef` and then runs `sha256(concat($1, col))` (`pkg/mask/hash.go:29-40`).
- `pii-masking.md:109-110` next to the upgrade section: WHERE, GROUP BY and joins "see the masked value". For post-query masks (`hash` with `hmac_sha256`, `fake`) such use is refused with `ERR_MASK_UNSUPPORTED_CONTEXT` 400 (`internal/queryservice/maskedrows.go:90`). Verified. The fragment comment "joinable" at `:129` is wrong inside Sluice.
- `examples/hello-sluice/seed.sql:26` comment: `ERR_ACL_DENIED`. Code: `ACL_DENIED` (`pkg/errors/codes.go:25`).
- `examples/pii-masking/seed.sql:4-6` comment: partial and hash "downgrade to null until pkg/mask registers them". Both are registered and work (`pkg/mask/partial.go`, `pkg/mask/hash.go`).

### Broken code blocks
- `multi-tenant.md:80`: hashes for ids `acme` and `beta` do not match the example bindings. `docker compose` and `sqlite3` are not available here. The checked-in hashes already work.
- `multi-tenant.md:90`: `X-Api-Key: acme.supersecret` gives 401. With `sl_demo_acme.supersecret` the shown output is exact.
- `break-glass.md:20`: binding part differs from `bindings.yaml` (names and key ids).
- `break-glass.md:75`: both keys give 401. With `sl_demo_analyst.supersecret` and `sl_demo_sre.rotatedtoken` the shown outputs are exact.
- `cross-source-joins.md:86` and `:98`: wrong key id (401 on the example). Needs Docker, Postgres, MinIO, not run.
- `mcp-agents.md:16`: exits 1 with the example `server.yaml` (pinned credential check). From another working directory it also fails on `./data/shop.db` (relative path).
- `mcp-agents.md:47`: validates, but is not the example content (extra policy, extra binding fields).
- `examples/mcp-agent/claude-desktop.json`: `serve` without credential exits 1. Hash `c3d4e5f6...` is a placeholder, the real one for `mcp.supersecret` is `341081ca3a9ec503c1ee5ab902f514de90c9978a103d67ee43ed836247da40bb`.
- `examples/mcp-agent/README.md:51-57`: inspector command with `sluice serve` exits 1 for the same reason.
- `examples/hello-sluice/README.md:64-70` ("Without Docker"): exits 1, `stat /data/shop.db: no such file or directory`. Paths are container paths and the three env vars are not set.
- `examples/approval-workflow/run.sh`: needs `jq` and reads the accept URL from the keyboard. Cannot run unattended.

### Proposed pages for this section
All six recipes stay. Each needs a runnable example with golden output.
1. `cookbook/index.md`: one table (recipe, policy kinds, example directory) and three lines on how to run an example locally. Drop the time column, "how the recipes work" and "suggested order".
2. `cookbook/multi-tenant.md`: stay. Runs offline today. Add the join and subquery proof. Move the "common error" block to `policies/row-filters.md` or drop it.
3. `cookbook/pii-masking.md`: stay. Runs offline today. Move the provider list, `fakeType` values and the sha256 pitfall to `policies/column-masks.md` (duplicate). Keep one upgrade variant only if it gets its own tested files.
4. `cookbook/break-glass.md`: stay. Runs offline today. Move exclude scope rules to `policies/matching.md`. Say what the audit record really shows.
5. `cookbook/approval-workflow.md`: stay, but only after the example becomes self-contained (see example block). Move config keys, state rules and status codes to `policies/approvals.md`. State the `SELECT *` limit.
6. `cookbook/cross-source-joins.md`: stay. Rebuild on `sqlite` plus `duckdb_file` so it runs offline. Keep Postgres plus S3 as a second, untested compose variant or drop it.
7. `cookbook/mcp-agents.md`: stay. Fix the example first. Move the tool list to `reference/mcp.md`.

### Features the code has that the section does not document
- Policy loader skips `tests/`, `testdata/` and dot directories, so suites can live in `policies.d/tests/` (`internal/config/policies.go:176`).
- `sluice policy test --tests <path>` and `--json` (`sluice policy test --help`).
- `mcp.apiKeyRef`, `mcp.tokenRef`, `mcp.allowAnonymous` for the stdio transport inside `sluice serve` (`internal/config/server.go:51-63`).
- `budget.enabled` server switch and `sluice budget show` (`cmd/sluice/budgetwire.go:20`).
- `GET /admin/approvals` (pending list, no tokens) and `GET /v1/approvals/{id}` (status for the requester) (`internal/transport/admin/routes.go:23`, `internal/transport/rest/routes.go:47`).
- Approval decision by `POST` and by `X-Approval-Token` header (`internal/transport/rest/routes.go:44`, `approvals.go:97`).
- 202 body carries `details.approval_id` and `details.expires_at` (`internal/queryservice/approval.go:107-111`).
- `Authorization: ApiKey <id>.<secret>` as a second header form (`internal/identity/apikey.go:185-195`).
- `duckdb_file` data source type, no extension needed (`pkg/apitypes/datasource.go:63`, `internal/datasource/drivers/duckdbfile/driver.go:74`).
- Unqualified table names are refused: `customers` gives `ACL_DENIED`, `shop.customers` gives `ACL_REJECTED` "is ambiguous, write catalog.schema.table" (`internal/queryservice/execute.go:169`).

### Code behaviour found on the way (may be bugs, not only doc errors)
- `ApprovalPolicy.when.columnsAccessed` does not see columns reached through `SELECT *` or `t.*`. The recipe policy lets `ssn` out without approval (`internal/policy/approval.go:55-60`). Verified.
- MCP `list_tables` and `list_accessible_tables` are empty until the catalog has been loaded into the schema cache, and again after each policy reload (`cmd/sluice/bootstrap.go:499` calls `InvalidateAll`). Verified for the cold start, the reload part is read only.
- Audit `policies_applied` lists a `ColumnMaskPolicy` whose `exclude` lifted it for that query. Verified.
- A `budget:` block on a `SubjectBinding` is accepted and ignored when `budget.enabled` is false. No warning at load. Verified.
- A `ColumnMaskPolicy` without `resources.columns` validates and masks nothing. No warning. Verified.

## Notes for the harness builder (all examples)
- SQLite sources need the DuckDB extension `sqlite_scanner`. The default build loads it from `~/.duckdb/extensions/v1.5.5/linux_amd64/` and downloads it when missing (`internal/datasource/drivers/common/extensions.go:23-45`). This machine has it cached. For a clean offline run use `make build-static` (compiles it in) or seed the directory with `scripts/fetch-duckdb-extensions.sh`. `duckdb_file` needs no extension.
- `make docs-check` already exists (generator drift check, `Makefile:138`). The new target must extend it or take another name.
- Path replacement that worked: in `server.yaml` and `policies.d/*.yaml` replace `/data/` and `./data/` with `<scratch>/data/`, `listen: ":8080"` and `":9091"` with free ports, `directory: "./policies.d"` with the scratch path. Script: `/tmp/audit-cookbook/bin/setup.sh`.
- Seeding without the `sqlite3` CLI: `python3 -c "import sqlite3; c=sqlite3.connect(db); c.executescript(open('seed.sql').read()); c.commit()"`.
- Start: `sluice serve --config <scratch>/server.yaml --policies-dir <scratch>/policies.d` with the env vars from `docker-compose.yaml`. Ready when `GET /v1/version` answers. Log line: `"msg":"sluice: starting"`.
- Audit records are flushed every 200 ms (`internal/audit/dispatcher.go:83`). `sluice audit verify` right after a query can still show only the genesis record. Wait 300 ms or stop the server first.
- A 401 writes no audit record.
- Volatile fields in outputs: `query_id`, `approval_id`, tokens, timestamps, hashes, `duration_ms`, `remote_ip`. Golden files should hold `columns` and `rows`, or mask these fields.
- `sluice policy test <dir>` needs a `schema:` block for every masked table. Without it: `rewrite error: rewriter: table columns not in the schema cache ... add a schema: block` and exit 3.
- No example directory has a suite file today except `examples/policies/tests/`.
- `claims.groups: "groups"` in the bindings of hello-sluice, pii-masking and cross-source-join is dead config for API keys. It is only used as a single literal group when the key has no `groups` (`cmd/sluice/bootstrap.go:770-773`). Remove it from the examples.

## docs/cookbook/index.md
Prose words now: 312. Suggested after rewrite: 110.

### Claims
| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-6 | Each recipe maps 1:1 to a runnable directory under `examples/` | mismatch | approval-workflow has only `policies.d/approval.yaml`, `run.sh`, README. mcp-agent exits 1 as shipped (`internal/config/validate.go:102`) |
| 2 | 6-7 | The YAML on the page is the configuration you run | mismatch | Drift in multi-tenant, break-glass, mcp-agents. Page-only policy in cross-source-joins |
| 3 | 7-9 | Every recipe ends with `sluice policy test` or two curl calls | mismatch | approval-workflow and mcp-agents have neither. break-glass and cross-source describe the test in prose only |
| 4 | 13-20 | Policy kinds per recipe | mismatch | Kinds exist (`pkg/apitypes/typemeta.go:21-28`). MCP row (rate limit, budget, QueryRewritePolicy) and cross-source row (QueryRejectPolicy) are not in the example directories |
| 5 | 13-20 | Time column (about 10 or 15 min) | fluff | Not measurable |
| 6 | 24-32 | Every recipe has the same five parts | fluff | Three pages have no "Ingredients" part |
| 7 | 34-35 | Recipes assume a built `sluice` binary | duplicate | Owner: getting-started/quickstart.md |
| 8 | 35-37 | Default deny: an empty policy directory denies everything | implemented (internal/policy/conflict.go:150) | Verified: `policy test` on an empty directory gives `deny` |
| 9 | 41-49 | Suggested order | fluff | Repeats the table |

### Code blocks
None.

## docs/cookbook/multi-tenant.md
Prose words now: 333. Suggested after rewrite: 220.

### Claims
| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-7 | Each API key sees only its tenant rows through one `RowFilterPolicy` | implemented (internal/rewriter/inject_filter.go:22) | Verified with two keys, see example block |
| 2 | 11-15 | Ingredients: one allow, one row filter, one binding per tenant, sqlite source `shop` with three tenants | implemented (examples/multi-tenant/policies.d/) | 5 objects, validate OK |
| 3 | 19-65 | Policy YAML equals the example | mismatch | Key ids `acme`, `beta` at lines 57 and 65. Example: `sl_demo_acme`, `sl_demo_beta`. Rest is equal |
| 4 | 68-70 | In API key mode `claims` values are stamped as literal claims | implemented (cmd/sluice/bootstrap.go:786-797, internal/identity/apikey.go:164-174) | |
| 5 | 70-72 | Template renders as bound `$1`, value never in the SQL text | implemented (internal/policy/template.go:68) | Verified: `rewrittenSqlContains: ["tenant_id = $1"]` passes |
| 6 | 76-78 | Compose hashes are placeholders, replace them | mismatch | They are valid for `sl_demo_acme` and `sl_demo_beta` with material `supersecret` |
| 7 | 97-98 | beta sees rows 3 and 4 only | implemented | Verified: `[[3,"beta","b1@beta.example"],[4,"beta","b2@beta.example"]]` |
| 8 | 100 | Rewritten SQL text | implemented | Verified exact through `rewrittenSqlContains` |
| 9 | 104-105 | gamma has rows but no binding, nobody can reach them | implemented | Verified: unknown key 401, acme and beta never see gamma rows |
| 10 | 109 | Suite in `policies.d/tests/` runs with `sluice policy test policies.d` | implemented (internal/config/policies.go:176, internal/policytest/policytest.go:197) | Verified. `serve` and `validate` skip the `tests` directory |
| 11 | 131-156 | Old dialect is rejected with `unknown kind sluice.bino-bi.github.io/v1/RowFilterPolicy`, then `spec.match: either any or all must be non-empty` | implemented | Verified both messages, exit 3. With `--strict` the second error is `unknown field "predicate"` |
| 12 | 131-133 | "Old drafts and third-party posts" show that dialect | fluff | Not a product fact |
| 13 | 160-162 | See also links | implemented | Files exist |

### Code blocks
| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 19 | yaml | allow, row filter, two bindings | broken | Bindings differ from example (key ids). `policy validate --strict` OK |
| 80 | bash | seed, `apikey hash`, compose up | broken | Flags are real. Ids `acme`, `beta` give hashes that do not fit the example. Docker and `sqlite3` not available |
| 90 | bash | curl as acme with output | broken | 401 with `acme.supersecret`. Output exact with `sl_demo_acme.supersecret`. `jq` not installed here |
| 100 | sql | rewritten SQL | untested, verified by hand | Exact match |
| 111 | yaml | suite `isolation.yaml` | untested, verified by hand | 2 passed against the example policies |
| 135 | yaml | broken dialect sample | untested, verified by hand | Fails as described |

## docs/cookbook/pii-masking.md
Prose words now: 424. Suggested after rewrite: 250.

### Claims
| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-7 | Analysts never see plain `email`, `phone`, `ssn`. `birth_date` stays visible | implemented | Verified, see example block |
| 2 | 11-12 | One allow plus three `ColumnMaskPolicy` objects | implemented (examples/pii-masking/policies.d/) | Page YAML equals the 4 example files |
| 3 | 12-13 | A mask without `resources.columns` matches nothing | implemented (internal/policy/conflict.go:309-317, internal/policy/selector.go:333) | Verified: validates, `masks: []`. No warning is given |
| 4 | 17-19 | Without the allow, default deny blocks before any mask | implemented (internal/policy/conflict.go:150) | |
| 5 | 83-84 | Priority matters only when two masks hit one column, highest wins | implemented (internal/policy/conflict.go:295-303) | Read |
| 6 | 88-96 | Before and after table | implemented | Verified against the live run |
| 7 | 98-107 | Rewritten SQL reads a subquery that lists columns and applies masks | implemented (internal/rewriter/substitute_mask.go) | Verified exact through `rewrittenSqlContains` |
| 8 | 109-110 | WHERE, GROUP BY and joins on a masked column see the masked value | mismatch | True for `null`, `constant`, `partial` (verified). Post-query masks refuse it: `ERR_MASK_UNSUPPORTED_CONTEXT` 400 (`internal/queryservice/maskedrows.go:90`) |
| 9 | 110-112 | Wrapper needs the table in the schema cache, the server reads it from the data source | internal detail | `internal/rewriter/errors.go:32`. Keep only the `schema:` hint for tests |
| 10 | 116-118 | `partial` rewrites SQL. `hash` with `hmac_sha256` and `fake` run post-query | implemented (pkg/mask/partial.go:23, pkg/mask/hash.go:67, pkg/mask/fake.go:40) | Verified on a live server |
| 11 | 143-145 | `fakeType` values: name, first_name, last_name, email, phone, company, city, country, uuid | duplicate | Correct (`pkg/mask/fake.go:15-23`). Owner: policies/column-masks.md |
| 12 | 149 | Suite path and command | implemented | Verified |
| 13 | 169 | Without `schema:` the case fails with a rewrite error that asks for it | implemented (internal/policytest/policytest.go:303) | Verified, exit 3 |
| 14 | 170-173 | `expect.masks` lists all winners, `expect.postMasks` the two post-query ones | implemented (internal/policytest/policytest.go:74-75) | Verified with the three fragments |
| 15 | 177-179 | `hash` default is `sha256`, in SQL, unkeyed | mismatch | Default and SQL path correct. `sha256` accepts `saltRef` (`pkg/mask/hash.go:29-40`) |
| 16 | 179-181 | `hmac_sha256` requires `saltRef` and runs post-query | duplicate | Correct (`pkg/mask/hash.go:55-58,67`). Owner: policies/column-masks.md |
| 17 | 181-184 | Store the salt through `secret://env/` or `secret://file/`. Rotation changes every output | implemented (internal/secrets/resolver.go:60-61) | Rotation advice is fluff |
| 18 | 188-190 | See also links | implemented | Files exist |

### Code blocks
| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 21 | yaml | allow plus three masks | untested, verified by hand | Equal to the example files. Live output matches |
| 101 | sql | rewritten SQL | untested, verified by hand | Exact match (page adds line breaks) |
| 120 | yaml | fragment `partial` | untested, verified by hand | Live: `al***************` |
| 127 | yaml | fragment `hash` hmac | untested, verified by hand | Live: 64 hex chars. Needs env `SLUICE_MASK_SALT` on the server. Comment "joinable" is wrong inside Sluice |
| 136 | yaml | fragment `fake` phone | untested, verified by hand | Live: `+1-555-999-9027` |
| 151 | yaml | suite `masks.yaml` | untested, verified by hand | 1 passed |

## docs/cookbook/break-glass.md
Prose words now: 346. Suggested after rewrite: 230.

### Claims
| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-8 | SRE sees raw `email`, other analytics users see it masked, no reload, every query is in the audit log | implemented (internal/policy/conflict.go:284) | Verified live. Also covered by `internal/policy/regression_test.go:73` with inline policies |
| 2 | 12-16 | One shared allow, one mask with `exclude`, two bindings, SRE key has `analytics` and `sre` | implemented (examples/break-glass/policies.d/) | |
| 3 | 20-66 | Policy YAML equals the example | mismatch | Mask is equal. Binding names and key ids differ (lines 46, 51, 57, 63) |
| 4 | 68-71 | SRE matches the allow through `analytics`. `sre` alone grants nothing | implemented | Verified: `policy test` with groups `[sre]` gives `deny` |
| 5 | 89-93 | As a test: analyst `masks: [...email=null]`, SRE `masks: []`, suite needs `schema:` | implemented | Verified, 3 passed |
| 6 | 97-98 | Both queries go to the same hash chain with subject and groups | mismatch | Only the `query` record has the subject. `query-result` has `"subject":{}` |
| 7 | 100-103 | `GET /admin/audit/tail?n=20` with bearer token | implemented (internal/transport/admin/routes.go:22, audit.go:25) | Verified, answer is JSON lines. 401 without token |
| 8 | 106 | `sluice audit verify`, exit code 4 means broken chain | implemented | Verified: tampered copy gives `hash mismatch`, exit 4 |
| 9 | 107 | jq filter on `.subject.groups` with `index("sre")` finds elevated queries | implemented | `jq` not installed. Same filter in python finds exactly the one SRE `query` record |
| 10 | 110-112 | Feed the audit directory into a SIEM and alert | fluff | Advice |
| 11 | 116-119 | For mask and row filter kinds the exclude is applied per table | implemented (internal/policy/engine.go:174-178, conflict.go:217,284) | |
| 12 | 119-120 | Add `resources:` to the exclude to lift the mask for some tables only | implemented | Verified with a two-table join |
| 13 | 120-121 | For access and reject kinds a matching exclude drops the policy for the whole query | mismatch | For `SqlAccessPolicy` the exclude is applied per table unless it has an `all` block or covers every table (`internal/policy/engine.go:179-187`). Owner: policies/matching.md |
| 14 | 122-127 | The exclusion is always on. `ApprovalPolicy` is time-boxed, single-use, accepted by a second person | implemented | "Second person" is not enforced. Anyone with the accept URL can accept |
| 15 | 131-133 | See also links | implemented | Files exist |

### Code blocks
| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 20 | yaml | mask with exclude, two bindings | broken | Bindings differ from `bindings.yaml`. `policy validate --strict` OK |
| 75 | bash | two curls with output | broken | Keys `analyst.supersecret`, `sre.rotatedtoken` give 401. With the example keys both outputs are exact |
| 100 | bash | audit tail, verify, jq | untested, verified by hand | `$ADMIN_TOKEN` is `demo-admin-token-change-me` in the example. `jq` part emulated |

## docs/cookbook/approval-workflow.md
Prose words now: 440. Suggested after rewrite: 300.

### Claims
| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-8 | Query is parked, webhook carries accept and reject links, identical query runs once after acceptance | implemented (internal/queryservice/approval.go:55-112) | Verified end to end on a self-built setup |
| 2 | 12-13 | Approvals gate allowed queries | implemented (internal/policy/approval.go:10-15) | |
| 3 | 15-38 | Policy YAML equals the example | implemented | Equal to `approval.yaml`, validate OK |
| 4 | 40-41 | `when` conditions are OR'd: `ssn`, `salary*` or `country = 'de'` | mismatch | OR is correct (`internal/policy/approval.go:55-68`, verified). `SELECT *` returns `ssn` without approval. `IN ('de')` and `lower(country) = 'de'` do not trigger |
| 5 | 41-42 | Empty `when` triggers on the selector alone | implemented (internal/policy/approval.go:48) | Read |
| 6 | 48-58 | Config keys `publicBaseUrl`, `syncWait`, `requestTtl`, `grantTtl`, `webhooks[].url`, `headersRef` | duplicate | Correct (`internal/config/server.go:241-265`, defaults 20s, 15m, 5m at `:355-363`). Owner: policies/approvals.md |
| 7 | 60-61 | `publicBaseUrl` is required once an `ApprovalPolicy` loads, server refuses to start | implemented (cmd/sluice/approval.go:39-41) | Verified: `serve` exit 1, `config validate` exit 3 |
| 8 | 65-66 | `run.sh` drives the flow | mismatch | Needs `jq` and a pasted URL. No server config, data or binding in the directory |
| 9 | 68-70 | After `syncWait` the answer is HTTP 202, `ERR_APPROVAL_PENDING`, with an `approval_id` | implemented (pkg/errors/codes.go:82) | Verified. Id is in `details.approval_id` |
| 10 | 71-85 | Webhook payload fields | mismatch | Fields exist (`internal/approval/webhook.go:51-61`). Real `subject` has no `email` key when empty, `issuer` is the binding name |
| 11 | 87-88 | The token in the URL is the only authorisation | implemented (internal/transport/rest/approvals.go:14-18) | Verified: bad token and unknown id give the same 404 |
| 12 | 89-92 | Identical query runs once, the next run pends again | implemented | Verified |
| 13 | 94-95 | Same verb twice is idempotent, opposite verb gives 409 | implemented (internal/transport/rest/approvals.go:45-49) | Verified |
| 14 | 95-96 | Rejected gives `ERR_APPROVAL_REJECTED` 403, expired gives `ERR_APPROVAL_EXPIRED` 410 | mismatch | Only inside the waiting request (`internal/queryservice/approval.go:91-102`). Verified 403 and 410 that way. A later re-run gives 202 |
| 15 | 100-104 | MCP: `await_approval {approval_id, timeout_seconds}` max 55 s, `check_approval` snapshot | implemented (internal/transport/mcp/tools.go:101-114) | Verified over stdio with `sluice serve`. `sluice mcp` alone has no REST listener for the accept URL (`cmd/sluice/mcp.go:26-29`) |
| 16 | 108-110 | Links are built from `publicBaseUrl` | implemented (internal/approval/webhook.go:133-135) | Verified |
| 17 | 111-114 | Delivery is retried 3 times with backoff, then the request stays pending | duplicate | Correct (`internal/approval/webhook.go:94-110`). Owner: policies/approvals.md |
| 18 | 115-116 | `grantTtl` default 5m, starts at acceptance | duplicate | Default correct (`internal/config/server.go:358`). Owner: policies/approvals.md |
| 19 | 117-121 | State is in memory, `approval.persist: true` keeps it in SQLite under `approval.stateDir`, single instance | duplicate | Correct (`cmd/sluice/approval.go:56-67`). Owner: policies/approvals.md |
| 20 | 125-126 | See also links | implemented | Files exist |

### Code blocks
| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 15 | yaml | allow plus approval policy | untested, verified by hand | Equal to example, drove a real flow |
| 48 | yaml | `approval` config fragment | untested, verified by hand | Same keys worked with local URLs. `headersRef` not exercised |
| 73 | json | webhook payload | broken | Real `subject` is `{"id":"alice","issuer":"analysts","groups":["analysts"]}` |

## docs/cookbook/cross-source-joins.md
Prose words now: 360. Suggested after rewrite: 240.

### Claims
| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-7 | Join Postgres rows with S3 Parquet in one SELECT under one policy set | implemented | Not run (needs Docker). Same join verified with `sqlite` plus `duckdb_file` |
| 2 | 11-12 | Both catalogs are DuckDB attachments, the engine treats them the same | internal detail | |
| 3 | 12-13 | S3 `allowedPaths` is an allow list enforced by the driver | implemented (internal/datasource/drivers/s3/driver.go:82-121,356) | Read only |
| 4 | 15-40 | DataSource YAML equals the example | implemented | Equal to `datasource-pg.yaml` and `datasource-s3.yaml`, validate OK |
| 5 | 45-47 | One clause per catalog because catalogs, schemas, tables constrain each other in a clause | implemented (internal/policy/selector.go) | Page YAML equals `allow-analytics.yaml` |
| 6 | 73-77 | A join with a table no allow names is denied with `no SqlAccessPolicy allows <table>` | implemented (internal/policy/conflict.go:166) | Verified live |
| 7 | 77-79, 107-109 | `limits.disableCrossCatalog: true` (default false) rejects with `ACL_REJECTED` before policy evaluation and before the rewrite cache | implemented (internal/queryservice/execute.go:127-140, internal/config/server.go:214) | Verified live: message `cross-catalog queries are disabled (limits.disableCrossCatalog)` |
| 8 | 110-111 | It sees a catalog only in three-part names | implemented (internal/queryservice/execute.go:128-130) | Read |
| 9 | 83-84 | Compose runs Postgres, MinIO with a one-shot seed job, and Sluice | implemented (examples/cross-source-join/docker-compose.yaml) | Not run |
| 10 | 86-94 | Join result rows | implemented | Not run on Postgres and S3. Offline variant returns the same 5 rows |
| 11 | 96-103 | Table outside the policy gives `ACL_DENIED` | implemented | Verified by `policy test` on the example policies and live on the offline variant |
| 12 | 114-131 | `QueryRejectPolicy` with `size(query.catalogs) > 1` | implemented | Verified live and by `policy test`. Not in the example directory |
| 13 | 133-135 | As a test: `outcome: reject`, `rejections: ["no-cross-catalog/single-catalog-only"]`, single catalog passes | implemented (internal/policytest/policytest.go:72) | Verified, 3 passed |
| 14 | 139-141 | `tables: ["*"]` without catalog or schema matches in every catalog | implemented | Used by `examples/approval-workflow/policies.d/approval.yaml:9`, verified there |
| 15 | 141-144 | `**` glob in `allowedPaths` makes every future object readable | implemented (internal/datasource/drivers/common/s3path.go:38) | Read only |
| 16 | 148 | "All six drivers" | mismatch | Seven types (`pkg/apitypes/datasource.go:58-64`) |
| 17 | 149-150 | See also links | implemented | Files exist |

### Code blocks
| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 15 | yaml | two DataSources | untested, not verified | Validates. Needs Postgres and MinIO |
| 49 | yaml | allow on both catalogs | untested, verified by hand | Equal to example. Used in `policy test` |
| 86 | bash | compose up, join curl with output | broken | Key id wrong (`analyst` instead of `sl_demo_analyst`). Docker not available. Output shape confirmed on the offline variant |
| 98 | bash | denied table curl | broken | Same key id problem. Code `ACL_DENIED` confirmed |
| 114 | yaml | `QueryRejectPolicy` | untested, verified by hand | Live: 403 `ACL_REJECTED`, `"policy":"no-cross-catalog"` |

## docs/cookbook/mcp-agents.md
Prose words now: 455. Suggested after rewrite: 300.

### Claims
| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-8 | MCP callers go through the same pipeline as other callers | implemented (internal/transport/mcp/tools.go:334-341) | Verified: deny, rate limit, audit all apply |
| 2 | 12-14 | stdio authenticates once at start and pins the identity | implemented (cmd/sluice/mcp.go:85-106) | Verified, log `mcp: authenticated static identity` |
| 3 | 16-34 | Claude Desktop config | mismatch | Flags are real. Fails with the example `server.yaml` (`internal/config/validate.go:102`). Example file uses `serve` |
| 4 | 37 | Paths must be absolute | mismatch | Example `server.yaml` and DataSource use `./data/...`, resolved against the working directory. From another directory: `stat .../data/shop.db: no such file`, exit 1 (verified) |
| 5 | 37-40 | `--jwt` or `SLUICE_MCP_TOKEN`, `--allow-anonymous` | implemented (cmd/sluice/mcp.go:89-104,134-136) | Read, `--help` confirms |
| 6 | 44-45 | Three layers, all hot-reloadable | implemented (internal/config/server.go:318) | `policies.reload: true`. Budget layer needs `budget.enabled` |
| 7 | 47-95 | Policy YAML equals the example | mismatch | `QueryRewritePolicy/agent-guardrails` is page only. Binding `rateLimit` and `budget` are page only |
| 8 | 76 | `limit.max: 500` injects LIMIT and clamps larger values | implemented (internal/rewriter/apply_rewrite.go:20-40) | Verified by `policy test` |
| 9 | 77 | `timeout: 10s` | implemented (internal/policy/composite.go:161, internal/queryservice/execute.go:309) | Read |
| 10 | 86-88 | `rateLimit` rps 2, burst 5 | implemented (internal/queryservice/execute.go:62-65) | Verified: sixth quick call gives `ERR_RATE_LIMITED` |
| 11 | 89-91 | `budget` rows and CPU seconds per day | mismatch | Needs `budget.enabled: true` in `server.yaml` (`cmd/sluice/budgetwire.go:20`). Not said, not set in the example |
| 12 | 98-100 | Masks apply to MCP callers as to REST callers | implemented | Same service path. Not run with a mask |
| 13 | 104-106 | Nine tools, names listed | duplicate | Correct (`internal/transport/mcp/tools.go:23-81`), verified by `tools/list`. Owner: reference/mcp.md |
| 14 | 109 | `whoami` gives subject `mcp-agent`, groups `["agents"]` | implemented (internal/transport/mcp/tools.go:152) | Verified |
| 15 | 110-111 | `list_accessible_tables` gives `shop.main.products` | mismatch | Empty until the catalog is in the schema cache (`internal/queryservice/tables.go:174`) |
| 16 | 112-114 | `explain_access {table}` shows policies, filters, masks | implemented (internal/transport/mcp/tools.go:203-231) | Verified |
| 17 | 115-116 | `execute_sql {sql, row_limit}` returns rows with LIMIT injected | implemented | Rows verified. LIMIT only with the page-only rewrite policy |
| 18 | 118-121 | Denied query returns `ACL_DENIED` as tool output, `explain_access` takes the SQL | implemented | Verified: `isError: true`, text `ACL_DENIED: no SqlAccessPolicy matched (default-deny)` |
| 19 | 121-122 | Every call lands in the audit log under the pinned subject | mismatch | Only `execute_sql` and `explain_access` are audited |
| 20 | 124-126 | Schema descriptions from a metadata store are not part of the tools | not implemented | The page says so itself. Looked at tool list and `describe_table` output |
| 21 | 126-127 | `describe_table` returns names, types, nullability | implemented (internal/transport/mcp/tools.go:477-500) | Verified |
| 22 | 127-129 | OTel spans when `tracing` is enabled | implemented (internal/config/server.go:184-186,455) | Read |
| 23 | 133-136 | An empty table list is a policy gap | mismatch | Also empty on a cold schema cache |
| 24 | 136-138 | Treat the allow list as the blast radius | fluff | Advice |
| 25 | 142-144 | See also links | implemented | Files exist |

### Code blocks
| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 16 | json | Claude Desktop config | broken | Exits 1 with the example `server.yaml`. Differs from `claude-desktop.json` |
| 47 | yaml | allow, rewrite policy, binding with limits | broken | Not the example content. Validates and works when added by hand |

## Example: examples/multi-tenant
- Files: README.md, docker-compose.yaml, server.yaml, seed.sql, data/.gitkeep, policies.d/{allow-analytics,bindings-tenants,datasource-shop,filter-tenant}.yaml. No suite file.
- `policy validate --strict`: OK, 5 objects. `config validate --config server.yaml`: OK.
- Needs: `sqlite` source (extension `sqlite_scanner`). Env: `SLUICE_APIKEY_PEPPER=multi-tenant-demo-pepper`, `SLUICE_AUDIT_GENESIS=multi-tenant-demo-genesis`, `SLUICE_APIKEY_ACME_HASH=5b42d0c2...65ea`, `SLUICE_APIKEY_BETA_HASH=04fdc1c0...b04a` (full values in `docker-compose.yaml:19-20`, they are valid). Seed: `seed.sql` into `data/shop.db`. No external service.
- Runs offline without Docker: yes. Steps: path and port replacement, python seed, env vars, `sluice serve`.
- Query from page and README, `SELECT id, tenant_id, email FROM shop.main.customers ORDER BY id`:
  - `sl_demo_acme.supersecret`: 200 `{"columns":["id","tenant_id","email"],"rows":[[1,"acme","a1@acme.example"],[2,"acme","a2@acme.example"]],"row_count":2,"truncated":false}` (plus `query_id`). Matches the page output.
  - `sl_demo_beta.supersecret`: `[[3,"beta","b1@beta.example"],[4,"beta","b2@beta.example"]]`. Matches the README comment.
  - Page keys `acme.supersecret`, `beta.supersecret`: 401 `ERR_UNAUTHORIZED`. Broken.
- Isolation proof (same SQL, acme then beta):
  - `SELECT id, tenant_id, customer_id, amount_cents FROM shop.main.orders ORDER BY id`: `[[10,"acme",1,1999],[11,"acme",2,4599]]` and `[[12,"beta",3,7800],[13,"beta",4,250]]`.
  - Join `... FROM shop.main.customers c JOIN shop.main.orders o ON o.customer_id = c.id ORDER BY o.id` selecting `c.id, c.tenant_id, o.id, o.tenant_id, o.amount_cents`: `[[1,"acme",10,"acme",1999],[2,"acme",11,"acme",4599]]` and `[[3,"beta",12,"beta",7800],[4,"beta",13,"beta",250]]`.
  - Cross join customers x orders: 4 rows per tenant, own tenant only.
  - Subquery `SELECT id, tenant_id FROM shop.main.orders WHERE customer_id IN (SELECT id FROM shop.main.customers) ORDER BY id`: `[[10,"acme"],[11,"acme"]]` and `[[12,"beta"],[13,"beta"]]`.
  - Scalar subqueries `(SELECT count(*) FROM shop.main.orders)`: 2 for each tenant (5 rows in the table).
  - `WHERE tenant_id = 'gamma' OR 1=1`: own rows only. CTE, UNION ALL, `count(*)`, GROUP BY, `SELECT *`: own rows only.
  - `SELECT id FROM customers`: 403 `ACL_DENIED`. `SELECT id FROM shop.customers`: 403 `ACL_REJECTED`.
- Audit `query` record: `subject` `{"groups":["analytics"],"id":"acme-analyst","issuer":"tenant-acme","method":"api_key","remote_ip":...}`, `policies_applied` holds `SqlAccessPolicy/allow-analytics` and `RowFilterPolicy/filter-tenant`. The tenant value is not recorded.
- Suite for mode (a): the page suite passes as is (`--tests`), 2 cases. Add the join and subquery cases.
- Blocks: none for offline. Smallest change: fix the page key ids to `sl_demo_acme`, `sl_demo_beta`, add `policies.d/tests/isolation.yaml`.

## Example: examples/pii-masking
- Files: README.md, docker-compose.yaml, server.yaml, seed.sql, data/.gitkeep, policies.d/{allow-analytics,binding-apikey,datasource-shop,mask-email,mask-phone,mask-ssn}.yaml. No suite file.
- `policy validate --strict`: OK, 6 objects. `config validate`: OK.
- Needs: `sqlite` source. Env: `SLUICE_APIKEY_PEPPER=pii-masking-demo-pepper`, `SLUICE_AUDIT_GENESIS=pii-masking-demo-genesis`, `SLUICE_APIKEY_ANALYST_HASH=7b332af8...0d46` (valid for `sl_demo_analyst:supersecret`). Seed: 4 customers.
- Runs offline without Docker: yes, same steps as multi-tenant.
- Query from page and README, `SELECT id, email, phone, ssn, birth_date FROM shop.main.customers ORDER BY id` with `sl_demo_analyst.supersecret`: 200, rows `[[1,null,"+1-555-REDACTED","REDACTED","1988-03-14"],[2,null,"+1-555-REDACTED","REDACTED","1975-07-22"],[3,null,"+1-555-REDACTED","REDACTED","1990-11-30"],[4,null,"+1-555-REDACTED","REDACTED","2001-02-02"]]`. Matches README and the page table.
- Other real answers: `WHERE email LIKE '%alice%'` gives `[]`. `GROUP BY ssn` gives `[["REDACTED",4]]`. `SELECT *` returns all 7 columns with the three masks.
- Upgrade fragments (scratch copy with the three mask blocks swapped, extra env `SLUICE_MASK_SALT=demo-mask-salt`): row 1 is `[1,"al***************","+1-555-999-9027","d8edb3f3...a80f","1988-03-14"]`. `WHERE ssn = ...` and `GROUP BY phone` give 400 `ERR_MASK_UNSUPPORTED_CONTEXT`. The hash value depends on the salt, the fake value on the seed (empty here).
- Suite for mode (a): page suite passes, 1 case. It needs the `schema:` block.
- Blocks: none. Smallest change: add `policies.d/tests/masks.yaml`. Fix stale comments in `seed.sql:4-6` and `mask-ssn.yaml:4-5` ("v0.2"). The page never shows the API key, the README does.

## Example: examples/break-glass
- Files: README.md, docker-compose.yaml, server.yaml, seed.sql, data/.gitkeep, policies.d/{allow-analytics,bindings,datasource-shop,mask-email}.yaml. No suite file.
- `policy validate --strict`: OK, 5 objects. `config validate`: OK.
- Needs: `sqlite` source. Env: `SLUICE_APIKEY_PEPPER=break-glass-demo-pepper`, `SLUICE_AUDIT_GENESIS=break-glass-demo-genesis`, `SLUICE_APIKEY_ANALYST_HASH=dced9bb2...97f0`, `SLUICE_APIKEY_SRE_HASH=6b20dbd5...6b09` (both valid). Seed: 3 customers.
- Runs offline without Docker: yes.
- Query `SELECT id, email FROM shop.main.customers ORDER BY id`:
  - `sl_demo_analyst.supersecret`: `[[1,null],[2,null],[3,null]]`. Matches.
  - `sl_demo_sre.rotatedtoken`: `[[1,"alice@acme.example"],[2,"bob@acme.example"],[3,"carol@acme.example"]]`. Matches.
  - Page keys `analyst.supersecret`, `sre.rotatedtoken`: 401. Broken.
- Mechanism check: exclude on the mask selector works per table (`internal/policy/conflict.go:284`). Group comes from the key (`bindings.yaml:40`). `sre` alone is denied.
- `sluice audit verify data/audit` after the two queries: `chain OK (1 file(s), 5 record(s), last_hash=87268c0425e2db96…)`. README shows `last_hash=...`, count matches. Real output cuts the hash and ends with an ellipsis character.
- Audit record of the SRE query (`event_type: query`): `"subject":{"groups":["analytics","sre"],"id":"on-call-sre","issuer":"break-glass-sre","method":"api_key",...}`, `"decision":"allow"`, `"policies_applied":[SqlAccessPolicy/allow-analytics, ColumnMaskPolicy/mask-email]`, `sql_fingerprint` equals `rewritten_fingerprint` (`f90a934da548078a`). The analyst record has the same `policies_applied` and a different `rewritten_fingerprint` (`f999d82ad25160ab`). So the record shows the group, but not that the mask was lifted. The `query-result` records have `"subject":{}`.
- Tamper test: one changed byte gives `hash mismatch`, exit 4.
- Suite for mode (a): wrote one with `schema: shop.main.customers: [id, tenant_id, email, created_at]`, 3 cases (analyst masked, SRE `masks: []`, `sre` alone denied). All pass.
- Blocks: none. Smallest change: fix page binding names and key ids, add the suite, fix the two comments about a separate audit record.

## Example: examples/approval-workflow
- Files: README.md, run.sh, policies.d/approval.yaml (2 objects). No server.yaml, no seed, no DataSource, no binding, no compose.
- `policy validate --strict`: OK, 2 objects.
- Needs: any source with `shop.main.people` (columns `ssn`, `salary*`, `country`), a binding for key `sl_demo_alice.world` in group `analysts`, the `approval` config block, a webhook receiver.
- Runs offline without Docker: not as shipped. A self-contained version ran fine here (`/tmp/audit-cookbook/approval/`). What I added:
  - `policies.d/datasource-shop.yaml` (`sqlite`, `data/shop.db`), `policies.d/binding-alice.yaml` (id `sl_demo_alice`, `hashRef: secret://env/SLUICE_APIKEY_ALICE_HASH`, groups `[analysts]`, `claims.subjectId: alice`).
  - `seed.sql`: `people(id, name, ssn, salary_eur, country)`, 3 rows (two `de`, one `fr`).
  - `server.yaml` with `approval: {publicBaseUrl: http://127.0.0.1:<rest-port>, syncWait: 2s, requestTtl: 15m, grantTtl: 5m, webhooks: [{url: http://127.0.0.1:<hook-port>/hook}]}`.
  - Env: `SLUICE_APIKEY_PEPPER=approval-demo-pepper`, `SLUICE_AUDIT_GENESIS=...`, `SLUICE_APIKEY_ALICE_HASH=b275fcc7c1ede77a4b5a8041c24fc3e49521faeafc18ab33e11119da8c46432f` (for `sl_demo_alice:world` with that pepper).
  - Webhook receiver: 12 lines of python `http.server` that append each POST body to a file (`/tmp/audit-cookbook/bin/hook.py`). The harness reads `accept_url` from the last line.
- Config keys (all in `internal/config/server.go:241-265`): `approval.publicBaseUrl`, `webhooks[].url`, `webhooks[].headersRef`, `webhooks[].timeout`, `syncWait` (20s), `requestTtl` (15m), `grantTtl` (5m), `maxPending` (1000), `sqlSampleBytes` (2048), `persist` (false), `stateDir` (`./state`). `syncWait` is clamped to `rest.requestTimeout - 2s` (`cmd/sluice/approval.go:178-188`).
- Can the grant be given without a webhook: no. The accept token leaves the server only in the webhook body (`internal/approval/webhook.go:66-86`). `GET /admin/approvals` has no token. No CLI command. So the harness needs the receiver. With `persist: true` the SQLite store holds only SHA-256 hashes of the tokens (`internal/approval/sqlite_store.go:33-34`), so the file does not help either.
- Real flow (SQL `SELECT ssn FROM shop.main.people WHERE country = 'de'`, header `Authorization: ApiKey sl_demo_alice.world`):
  1. Submit: 202 after 2.0 s, `{"code":"ERR_APPROVAL_PENDING","message":"human approval required; a decision has been requested. Poll or await the approval, then re-run the IDENTICAL query within the grant window to execute.","query_id":"...","details":{"approval_id":"...","expires_at":"..."}}`.
  2. Webhook body: `{"approval_id":"...","subject":{"id":"alice","issuer":"analysts","groups":["analysts"]},"sql":"SELECT ssn FROM shop.main.people WHERE country = 'de'","reasons":["policy approve-pii-reads: PII columns on people require a data-steward sign-off"],"policies":["approve-pii-reads"],"accept_url":"http://127.0.0.1:18250/v1/approvals/<id>/accept?token=<43 chars>","reject_url":"...","requested_at":"...","expires_at":"..."}`.
  3. `GET accept_url`: 200 `{"approval_id":"...","note":"recorded","state":"approved"}`. Again: `"note":"already decided"`. Reject after accept: 409.
  4. Re-run: 200 `{"columns":["ssn"],"rows":[["111-22-3333"],["222-33-4444"]],"row_count":2,"truncated":false}`.
  5. Re-run again: 202 with a new `approval_id`.
  - HEAD or `Purpose: prefetch` on the accept URL: 204, no change. Status poll `GET /v1/approvals/<id>` with the key: 200 `{"approval_id","expires_at","state":"pending"}`, without key 401.
  - Reject inside `syncWait`: waiting request ends with 403 `ERR_APPROVAL_REJECTED`. Expiry inside `syncWait` (`requestTtl: 1s`): 410 `ERR_APPROVAL_EXPIRED`. Accept inside `syncWait`: the waiting request returns rows directly.
  - No approval needed: `SELECT name ... WHERE country = 'fr'` (200), `SELECT * FROM shop.main.people` (200 with `ssn` and `salary_eur`).
- MCP variant: ran with `sluice serve`, `mcp: {enabled: true, transport: stdio, apiKeyRef: secret://env/SLUICE_MCP_APIKEY}`. `execute_sql` gives `ERR_APPROVAL_PENDING` text with `details: {"approval_id":...}` (not in `structuredContent`), `check_approval` gives `state=pending`, `await_approval` returns `state=approved` 1.5 s later, the next `execute_sql` returns rows, the one after pends again.
- Suite for mode (a): `Expect` has no approval field (`internal/policytest/policytest.go:68-79`), but `applied` works. Verified, 3 cases pass: the PII query has `outcome: allow` and `applied: ["ApprovalPolicy/approve-pii-reads", "SqlAccessPolicy/allow-analysts"]`, the `fr` query and `SELECT *` have only `SqlAccessPolicy/allow-analysts`.
- Smallest change: add the four files above, replace `run.sh` with a script that starts the receiver, reads `accept_url` from its file and uses no `jq`.

## Example: examples/cross-source-join
- Files: README.md, docker-compose.yaml, server.yaml, seed/postgres.sql, seed/README.md, data/.gitkeep, policies.d/{allow-analytics,binding-apikey,datasource-pg,datasource-s3}.yaml. No suite file.
- `policy validate --strict`: OK, 4 objects. `config validate`: OK.
- Needs: `postgres` source (host `postgres:5432`, extension `postgres_scanner`, never compiled in), `s3` source (MinIO at `minio:9000`, extension `httpfs`), a seed job that installs `duckdb` and downloads `mc` from the network. Env: `SLUICE_APIKEY_PEPPER=cross-source-demo-pepper`, `SLUICE_AUDIT_GENESIS=cross-source-demo-genesis`, `SLUICE_APIKEY_ANALYST_HASH=6b76e522...aef8` (valid), `SLUICE_PG_PASSWORD=demo-pg-password`, `SLUICE_S3_CREDS={"key_id":"sluice-demo","secret":"demo-s3-password"}`.
- Runs offline without Docker: no. Not run.
- Mode (a) works today: example policies plus the page reject policy, suite with 3 cases (join rejected, single catalog allowed, `pg.public.pg_stat_activity` denied), all pass.
- Offline types in the code (`pkg/apitypes/datasource.go:58-64`, `internal/datasource/drivers/`): `sqlite` (file, needs `sqlite_scanner`), `duckdb_file` (file, no extension). `http` and `s3` need `httpfs` and a server. No type reads a local Parquet or CSV file directly.
- Same recipe with two offline sources: yes, verified. Setup in `/tmp/audit-cookbook/cross/`: catalog `shop` (`sqlite`, table `orders` with the 5 rows of `seed/postgres.sql`) and catalog `ref` (`duckdb_file`, table `countries`, 4 rows). Allow policy with clauses for `shop.main.orders` and `ref.main.countries`.
  - Join `SELECT o.id, o.amount_cents, c.name AS country, c.region FROM shop.main.orders o JOIN ref.main.countries c ON c.code = o.country_code ORDER BY o.id`: 200 `{"columns":["id","amount_cents","country","region"],"rows":[[1,1999,"United States","Americas"],[2,4599,"Germany","EMEA"],[3,7800,"France","EMEA"],[4,250,"United States","Americas"],[5,32000,"Japan","APAC"]],"row_count":5,"truncated":false}`. Same rows as the README "expected shape".
  - Table without allow: 403 `{"code":"ACL_DENIED","message":"no SqlAccessPolicy matched (default-deny)"}`. Join with an unallowed table: `no SqlAccessPolicy allows shop.main.secrets`.
  - With the page `QueryRejectPolicy` (hot reload through `POST /admin/reload`): 403 `{"code":"ACL_REJECTED","message":"queries may not span more than one catalog","policy":"no-cross-catalog"}`. Single-catalog queries still 200.
  - With `limits.disableCrossCatalog: true`: 403 `ACL_REJECTED`, `cross-catalog queries are disabled (limits.disableCrossCatalog)`.
- Block for the offline variant: the `.duckdb` file. No `duckdb` CLI here. I built it with a 20 line Go program that uses the repo dependency `github.com/duckdb/duckdb-go/v2` (`/tmp/audit-cookbook/mkduck/main.go`). Options for the harness: commit a small `ref.duckdb` fixture (about 0.5 MB, format tied to the DuckDB version in `scripts/DUCKDB_VERSION`), or add a small `go run` seed helper to the repo.
- Smallest change: new `policies.d` with `datasource-shop.yaml` (`sqlite`) and `datasource-ref.yaml` (`duckdb_file`), `seed.sql` for orders, the duckdb seed step, a suite file. Keep the compose stack as an optional variant or remove it. Fix the page key id.
- Stale texts: `seed/README.md:5-6` calls the seed service `duckdb-seed` (compose name is `seed-parquet`). `seed/postgres.sql:2` names a `postgres-init` service that does not exist. README:94 says the Parquet has 4 rows, correct.

## Example: examples/mcp-agent
- Files: README.md, claude-desktop.json, server.yaml, seed.sql, data/.gitkeep, policies.d/{allow-agents,binding-apikey,datasource-catalog}.yaml. No docker-compose, no suite file.
- `policy validate --strict`: OK, 3 objects. `config validate --config server.yaml`: fails, exit 3 (`mcp.enabled with transport=stdio requires a pinned credential`).
- Needs: `sqlite` source with relative path `./data/shop.db` (run from the example directory). Env: `SLUICE_APIKEY_PEPPER=mcp-agent-demo-pepper`, `SLUICE_AUDIT_GENESIS=mcp-agent-demo-genesis`, `SLUICE_APIKEY_MCP_HASH`. The hash in `claude-desktop.json:14` is a placeholder. Real value for `mcp.supersecret`: `341081ca3a9ec503c1ee5ab902f514de90c9978a103d67ee43ed836247da40bb`.
- `claude-desktop.json` against the real CLI: `sluice mcp` flags are `--config`, `--policies-dir`, `--transport`, `--jwt`, `--api-key`, `--allow-anonymous`. The file calls `serve` (flags `--config`, `--policies-dir` only) and passes no credential. Result: exit 1. The page version (`mcp` plus `--api-key`) also exits 1 because `server.yaml` has `mcp.enabled: true`.
- Two ways that worked:
  1. `sluice mcp --config server.yaml --policies-dir policies.d --api-key mcp.supersecret` after setting `mcp.enabled: false` (or deleting the `mcp:` block). Exits 0 when stdin closes. Best for the harness.
  2. `sluice serve` after adding `mcp.apiKeyRef: secret://env/SLUICE_MCP_APIKEY` and env `SLUICE_MCP_APIKEY=mcp.supersecret`. REST also listens, the process does not stop on stdin EOF.
- Runs offline without Docker: yes with fix 1. Working directory must be the example directory, or the paths in `server.yaml` and `datasource-catalog.yaml` must be absolute.
- Tools the recipe names: all nine exist (`internal/transport/mcp/tools.go:23-81`). `tools/list` returned: `await_approval, check_approval, describe_table, execute_sql, explain_access, list_accessible_tables, list_catalogs, list_tables, whoami`.
- Real stdio session (python driver `/tmp/audit-cookbook/bin/mcpdrive.py`, newline-delimited JSON-RPC, protocol `2025-03-26`):
  - `initialize`: `{"capabilities":{"logging":{},"tools":{"listChanged":true}},"protocolVersion":"2025-03-26","serverInfo":{"name":"sluice","title":"Sluice \u2014 policy-enforcing SQL gateway","version":"dev"}}`.
  - `whoami {}`: `structuredContent` `{"anonymous":false,"auth_method":"api_key","groups":["agents"],"issuer":"mcp-local","subject":"mcp-agent"}`.
  - `list_catalogs {}`: `{"catalogs":[{"healthy":true,"name":"shop","type":"sqlite"}]}`. README says `[{name: "shop", ...}]`, matches.
  - `list_tables {"catalog":"shop"}` first: `{"tables":[]}`. README says `{"tables": ["shop.main.products"]}`. Broken on a fresh process. After `describe_table`: `{"tables":["shop.main.products"]}`.
  - `list_accessible_tables {}` first: `{"tables":[]}`. Page says `shop.main.products`. Broken on a fresh process, correct after `describe_table`.
  - `describe_table {"table":"shop.main.products"}`: 5 columns `id BIGINT nullable`, `sku VARCHAR`, `name VARCHAR`, `price_cents BIGINT`, `stock BIGINT` (the last four not nullable).
  - `explain_access {"table":"shop.main.products"}`: `{"decision":"allow","matched":[{"kind":"SqlAccessPolicy","name":"allow-agents","priority":100}],"resource":"shop.main.products","subject":"mcp-agent"}`.
  - `execute_sql {"sql":"SELECT name, price_cents FROM shop.main.products ORDER BY price_cents DESC","row_limit":100}`: `{"columns":["name","price_cents"],"row_count":5,"rows":[["Black T-Shirt (M)",2499],["Ceramic Mug",1299],["Sticker Pack (L)",999],["A5 Notebook",799],["Black Ballpoint",199]],"truncated":false}` plus `query_id`.
  - `execute_sql {"sql":"SELECT usename FROM shop.main.pg_stat_activity"}`: `isError: true`, text `ACL_DENIED: no SqlAccessPolicy matched (default-deny) (query_id=...)`.
  - `check_approval {"approval_id":"nope"}`: `ERR_INTERNAL: approvals not configured`.
- With the page bundle added by hand (rewrite policy, rate limit): `policy test` shows `LIMIT 500` injected, `LIMIT 100000` clamped, `LIMIT 3` kept. Nine quick `execute_sql` calls: 5 pass, 4 give `ERR_RATE_LIMITED: per-subject rate limit exceeded`.
- Audit after the sessions: `query` and `query-result` for `execute_sql`, `admin-action` (`policy explain`) for `explain_access`, all with `origin: mcp` and subject `mcp-agent`. Nothing for the other tools. `audit verify`: chain OK.
- Smallest change: set `mcp.enabled: false` in `server.yaml` (or add `mcp.apiKeyRef`), change `claude-desktop.json` to `mcp` plus `--api-key=mcp.supersecret` and the real hash, add `agent-guardrails.yaml` and the `rateLimit` block if the page keeps them, add `budget.enabled: true` if the page keeps budgets, delete the `SLUICE_MCP_GROUPS` comment, call `describe_table` before the list tools in the scripted session (or fix the cold cache in code).

## Example: examples/hello-sluice
- Files: README.md, docker-compose.yaml, .gitignore, server.yaml, seed.sql, policies.d/{allow-analytics,binding-apikey,datasource-shop,filter-tenant,mask-email}.yaml. No suite file. Three policy files and `seed.sql` are loaded by `internal/queryservice/function_gate_duckdb_test.go:32` (into DuckDB, not SQLite).
- `policy validate --strict`: OK, 5 objects. `config validate`: OK.
- Needs: `sqlite` source at `/data/shop.db`, audit path `/data/audit` (absolute, unlike the other examples). Env: `SLUICE_APIKEY_PEPPER=hello-sluice-demo-pepper`, `SLUICE_AUDIT_GENESIS=hello-sluice-demo-genesis`, `SLUICE_APIKEY_HELLO_HASH=c0c9ab61...5153` (valid for `sl_demo_hello:world`).
- Runs offline without Docker: yes after path replacement. The README "Without Docker" block does not work as written (exit 1, `/data/shop.db` missing, env vars not set).
- README step 3, `SELECT id, email, tenant_id FROM shop.main.customers ORDER BY id` with `sl_demo_hello.world`: 200 `{"query_id":"...","columns":["id","email","tenant_id"],"rows":[[1,null,"acme"],[2,null,"acme"]],"row_count":2,"truncated":false}`. Matches.
- README step 4, `audit verify`: `chain OK (1 file(s), 3 record(s), ...)` once the 200 ms flush has run. Read too early it shows 1 record.
- README step 5, `SELECT * FROM shop.main.admin_users`: 403 `{"code":"ACL_DENIED","message":"no SqlAccessPolicy matched (default-deny)","query_id":"..."}`. Matches. Audit then has a `query` record with `decision: deny`.
- Smallest change: make the paths relative (`./data/...`) like the other examples, add an env file or export lines to the "Without Docker" block, add a suite file.

## Example: examples/policies
- Files: allow.yaml (3 objects: allow, row filter, partial mask), tests/basic.yaml, tests/select-star.yaml.
- `policy validate --strict`: OK, 3 objects.
- Needs nothing: no server config, no data, no env.
- `sluice policy test examples/policies`: 4 passed, 0 failed, exit 0. Not called by any CI workflow.
- No end-to-end mode possible (no DataSource, no binding).
