# Audit: Policies

Two parts, audited by two agents.

- Part A: index, matching, conditions, testing, access-control, row-filters, column-masks.
- Part B: guardrails, approvals, subjects, classification, rebac, opa.

---

# Part A

Pages: `docs/policies/` index, matching, conditions, testing, access-control, row-filters, column-masks.
Commit audited: `ad57e1d`. Binary: `/tmp/sluice-bin/sluice`.

How it was checked:

- Every YAML policy block: `sluice policy validate --strict` (all 28 full blocks pass, the 4 fragments pass when wrapped in a policy).
- Rewritten SQL, outcomes, masks, error codes: `sluice policy test` suites in `/tmp/audit-policies-a/`.
- Run-time behavior: a local server (hello-sluice config, SQLite, API key, REST on 127.0.0.1:18140), stopped at the end. "Ran live" in a row means this server.
- "Read only" in a row means the code was read and not run.

## Summary

### Not implemented

- matching.md:46 (also conditions.md:106, matching.md:28): `subjects.ipRanges`. On REST the remote address is stored as `host:port` (`internal/identity/apikey.go:177`, `jwt.go:151`), `net.ParseIP` fails and the clause never matches (`internal/policy/selector.go:457-475`). Ran live from 127.0.0.1 with `127.0.0.0/8`: no match. A deny or mask scoped by `ipRanges` never fires. An allow scoped by it admits nobody.
- conditions.md:23-25, row-filters.md:98: `request.remote_ip`, `request.user_agent`, `request.headers`. REST and MCP never set `QueryRequest.Facts` (`internal/transport/rest/query.go:48-57`, `internal/transport/mcp/tools.go:336-342`). In CEL the three values are always `""`, `""`, `{}` (`internal/policy/cel.go:139-142`). A `{{ request.* }}` template fails every query with HTTP 500 `ERR_INTERNAL`. Ran live. Only `sluice policy test` fills `headers` and `userAgent`.
- index.md:26: `AuditSink` kind. The manifest is decoded and validated (`pkg/apitypes/decoder.go:505-539`), but `Snapshot.AuditSinks` is never read (`internal/config/policies.go:131`). Sinks come from the server config (`cmd/sluice/auditwire.go:28`).
- conditions.md:106-109: the tip "prefer `ipRanges` for network checks". Neither `ipRanges` nor `request.remote_ip` works, so no network check works today.

### Docs and code disagree

- row-filters.md:49: `IsNull` / `IsNotNull`. Doc: `col IS NULL`. Code emits `col OPERATOR(IS) NULL` (`internal/rewriter/inject_filter.go:419-434`). DuckDB cannot parse it, every query on the table fails with HTTP 500 `ERR_INTERNAL`. Ran live. `policy validate` and `policy test` pass. No test covers it. Three doc blocks use it.
- row-filters.md:101-103, conditions.md:143-145: missing template variable. Doc: request is denied. Code: HTTP 500 `ERR_INTERNAL`, audit decision `error` (`inject_filter.go:297`, `internal/rewriter/errors.go:77`). Ran live. No rows are served.
- row-filters.md:94: `{{ subject.groups }}`. Renders a list (`internal/policy/template.go:107-108`), DuckDB cannot bind it, HTTP 500 `ERR_INTERNAL`. Ran live.
- matching.md:44, access-control.md:107: `subjects.apiKeys`. Doc: matches the key id. Code: matches the subject id (`selector.go:436-440`), which is `spec.claims.subjectId` of the binding when set, else the key id (`internal/identity/apikey.go:160-163`, `cmd/sluice/bootstrap.go:777`). Ran live.
- matching.md:8: empty selector. Doc: matches nothing, policy is inert. Code: empty `spec.match` is a load error (`decoder.go:541-549`), except for `QueryRewritePolicy` (`decoder.go:244`).
- matching.md:104-106: specificity. Doc: tie-break for every kind. Code: only column masks use it (`internal/policy/conflict.go:295-303`). Other kinds: priority, then name (`internal/policy/snapshot.go:157-162`).
- index.md:54-55: name "at most 63 characters". Code checks the pattern only (`decoder.go:222`, `262`). A 72 character name loads.
- index.md:62: "every enforcement kind" has `conditions`. `RelationshipPolicy` has none (`pkg/apitypes/policy_relationship.go:17-19`).
- index.md:71-72: shadow policies "never change the decision". A shadow policy whose condition errors still denies the request (`internal/policy/engine.go:166-200`). Ran live.
- index.md:157: `--strict` rejects unknown fields. Unknown keys under `spec.mask.args` pass (`pkg/apitypes/mask_args_json.go:78-91`). Ran.
- conditions.md:30-31: `subject.auth_method` values. Doc: `jwt`, `api_key`, `admin_token`, `none`. Code sets only `jwt` and `api_key` (`internal/identity/jwt.go:310`, `apikey.go:175`). Anonymous is `""`.
- testing.md:107: exit code on policy compile failure. Doc and `--help`: 3. Code: 1 (`cmd/sluice/policytest.go:53-63`). Ran.
- testing.md:7-8: "a passing suite reflects real enforcement behaviour". The runner does not execute SQL and has no auth method, remote address or server gates. `IsNull` filters pass in the runner and fail live.
- access-control.md:8-10: default deny "cannot be configured away". With `policies.engine: opa` the YAML policies are not evaluated at all, with `composite` another member can allow (`cmd/sluice/policyengine.go:23-46`, `internal/policy/composite.go:15-24`). Read only.
- row-filters.md:85: injected `WHERE` text. Real text has `sluice_src.` on every column and `OPERATOR(IS) NULL`.
- row-filters.md:137-138: "the tenant filter from the policy model (index.md)". index.md has no row filter.
- column-masks.md:143-144: args "validated again at rewrite". Validated once, at load (`snapshot.go:521-533`).

### Broken code blocks

- matching.md:28: `all` fragment with `ipRanges`. Loads, but the `ipRanges` clause never matches on a live server, so the selector never matches.
- conditions.md:85: `QueryRejectPolicy`. Loads, but rule `internal-network-only` reads `request.remote_ip`, which is always empty, so it rejects every query. Ran live with the same kind of rule.
- testing.md:29: suite format example. The one case cannot pass: `masks` and `postMasks` name tables the SQL does not read and `applied` leaves out the mask policies. Ran with matching policies: FAIL. The field names are right.
- row-filters.md:61: `filter-active-eu`. Loads, uses `IsNull`, every query on the table fails with 500.
- row-filters.md:141: combined `WHERE`. Real text: `WHERE sluice_src.tenant_id = $1 AND (sluice_src.deleted_at OPERATOR(IS) NULL AND (...) AND NOT ...)`. No parentheses around the first filter.
- row-filters.md:226: `hide-soft-deleted`. Uses `IsNull`.

### Proposed pages for this section

Keep all seven pages. No merge, no removal. Move text so each fact has one owner.

1. `policies/index.md`: object anatomy, kinds table with links, the four shared fields, `enforcementMode`, default deny, loading, `policy validate`. Owner of `enforcementMode` and shadow.
2. `policies/matching.md`: `any` / `all`, subject fields, resource fields, wildcards, `exclude` per kind, priority. Drop "Combining row filters" (owner: row-filters.md). State that specificity is for masks only.
3. `policies/conditions.md`: `spec.conditions` rules, variable table, the bool rule, the error rule, three examples. Drop "The row-filter subset" and "The same tenant filter, twice" (owner: row-filters.md).
4. `policies/access-control.md`: allow, deny, default deny, per-table cover, actions, read-only floor, order deny, reject, approval.
5. `policies/row-filters.md`: predicate ops, templates, CEL form, combine, limits. Keep the file path and the anchor `#sql-forms-refused-on-filtered-or-masked-tables`: the error message names the file (`internal/rewriter/dialect.go:334`, asserted in `parse_check_test.go:80`) and three pages link to the anchor (architecture/request-lifecycle.md:152, architecture/security-model.md:27, column-masks.md:138). Cut the table to form, example, fix.
6. `policies/column-masks.md`: stage table, one short block per mask type (args table, one example), winner rule, "Limits" with one line per refused group and its error code.
7. `policies/testing.md`: suite format, every `expect` field, schema fixtures, flags, exit codes, `--json` shape, a "What the runner does not check" list. Put it last in the nav, after the kinds. Keep anchor `#schema-fixtures-for-masked-tables` (cookbook/break-glass.md:93 links to it).

Do not document as working until fixed: `IsNull` / `IsNotNull`, `ipRanges`, `request.*`, `{{ subject.groups }}`.

### Features the code has that the section does not document

- `EXPLAIN` on a table with a row filter or mask is refused: 400 `ERR_UNSUPPORTED_SYNTAX` (`internal/rewriter/rewriter.go:180-183`). Ran live.
- A statement with no table (`SELECT 1`, `SHOW`, `SET`, `PRAGMA`) is denied unless an allow clause names no catalogs, schemas or tables (`selector.go:410-421`). Ran live.
- A clause `{}` matches every subject and table (`selector.go:390-398`). A selector with both `any` and `all` matches when one `any` clause matches or all `all` clauses match (`selector.go:290-311`).
- Not validated at load, silently never match or fall back: `actions` values (`select`), `jwtClaims[].op` (`equals`), `combine` (`Permissive` acts as restrictive). Ran all three.
- A `jwtClaims` claim name that contains a dot (URL style) cannot be addressed: the name is split on dots (`selector.go:158`).
- `policy validate` on a path that does not exist prints OK with 0 objects, exit 0 (`internal/config/policies.go:155-159`). Ran.
- An `exclude` with an `all` block on a `SqlAccessPolicy` drops the whole policy (`engine.go:185`, `206-208`).
- A CEL bool check needs a comparison: a bare `query.has_cte` is type `dyn` and fails to load (`internal/policy/cel.go:99-101`). Ran.
- `fpe`: the key must be 16, 24 or 32 bytes, else 500 `ERR_INTERNAL` when the column is selected. A value with one in-alphabet character cuts the response. Ran both live.
- A post-query mask that fails on a row (`jitter` on text, `fpe` on a short value) returns HTTP 200 with the JSON body cut after `"rows":[`. Ran live.
- A missing secret for `hmac_sha256` is 500 `ERR_INTERNAL` at query time. `policy validate` does not resolve secrets. Ran live.
- Caller `$n` parameters are refused: `ERR_UNSUPPORTED_SYNTAX` (`rewriter.go:175-177`). Read only.

## docs/policies/index.md

Prose words now: 517. Suggested after rewrite: 300.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-7 | All enforcement is declared in YAML objects, compiled into an immutable snapshot | fluff | Not exact either: `policies.engine: opa` skips the YAML kinds (`cmd/sluice/policyengine.go:27-36`), server `limits.*` gates also enforce |
| 2 | 9-12 | `apiVersion` is `sluice.bino.bi/v1alpha1`, `v1beta1` for `DataClassification` and `RelationshipPolicy`, other values rejected at load | implemented (pkg/apitypes/typemeta.go:10-14) | Registry `decoder.go:33-53`. Ran: "unknown kind", exit 3 |
| 3 | 14 | "The 11 kinds" | fluff | Count goes stale. 11 is right today (`decoder.go:41-52`) |
| 4 | 18 | `SqlAccessPolicy`: allow or deny, no matching allow means denied | implemented (internal/policy/conflict.go:105-170) | Owner: access-control.md |
| 5 | 19 | `RowFilterPolicy` injects a WHERE predicate | implemented (internal/rewriter/inject_filter.go:256-319) | |
| 6 | 20 | `ColumnMaskPolicy` replaces values (null, partial, hash, FPE, fake) | implemented (pkg/mask/registry.go:85-96) | |
| 7 | 21-23 | `QueryRejectPolicy`, `QueryRewritePolicy`, `ApprovalPolicy` purposes | implemented (internal/policy/snapshot.go:334-377) | Details: part B |
| 8 | 24-25 | `DataSource`, `SubjectBinding` kinds | implemented (pkg/apitypes/decoder.go:47-48) | Details: other sections |
| 9 | 26 | `AuditSink`: where the audit trail is written | not implemented | Decoded, never read: `internal/config/policies.go:131`, `cmd/sluice/auditwire.go:28` |
| 10 | 27-28 | `DataClassification`, `RelationshipPolicy` are `v1beta1` | implemented (pkg/apitypes/decoder.go:51-52) | Ran: `v1alpha1` on `DataClassification` is rejected |
| 11 | 32 | Four top-level fields | implemented (pkg/apitypes/policy_access.go:7-11) | |
| 12 | 38, 54-55 | `metadata.name` is an RFC 1123 label, at most 63 characters | mismatch | Pattern checked, length not: `decoder.go:222`, `262`. Ran: 72 characters load |
| 13 | 55-56 | kind/name unique in the directory, duplicate is a load error | implemented (internal/config/policies.go:111-120) | Ran |
| 14 | 39, 57 | `metadata.priority` is an integer 0 to 1000 | implemented (pkg/apitypes/decoder.go:228-233) | Ran 1001 |
| 15 | 57-58 | Order: priority descending, then name ascending | implemented (internal/policy/snapshot.go:157-162) | |
| 16 | 40-41 | `labels` optional, informational | implemented (pkg/apitypes/typemeta.go:44) | No reader. `namespace` and `annotations` exist too, unused |
| 17 | 62-68 | Every enforcement kind has `match`, `exclude`, `conditions`, `enforcementMode` | mismatch | `RelationshipPolicy` has no `conditions`: `policy_relationship.go:17-19` |
| 18 | 69 | `enforcementMode`: `Enforce` (default), `Audit`, `DryRun` | implemented (internal/policy/snapshot.go:402-412) | Ran `audit`: load error |
| 19 | 71-74 | `Audit` and `DryRun` are recorded as shadow, shown as `shadow` in `policy explain --json`, both behave the same | implemented (internal/policy/conflict.go:41-45) | JSON tag `pkg/apitypes/dto.go:68`. Ran |
| 20 | 72 | Shadow policies never change the decision | mismatch | A shadow policy with a condition that errors denies the request: `engine.go:166-200`. Ran live: 403 `ACL_DENIED` |
| 21 | 110 | Empty directory is valid and means deny everything | implemented (internal/policy/conflict.go:145-153) | Ran. A missing path validates too |
| 22 | 111-112 | Minimum set: `DataSource`, `SubjectBinding`, allow `SqlAccessPolicy` | implemented (internal/identity/middleware.go:44-51) | REST gives 401 without a credential |
| 23 | 145-148 | Loads `*.yaml` / `*.yml` under `policies.directory` (default `./policies.d`), recursive, skips dot directories, `testdata`, `tests`, many objects per file with `---` | implemented (internal/config/policies.go:149-198) | Default `internal/config/server.go:317` |
| 24 | 153-154 | `sluice policy validate <dir> --strict` and its output line | implemented (cmd/sluice/policy.go:36-87) | Ran |
| 25 | 157-158 | `--strict` rejects unknown YAML fields | mismatch | Unknown keys in `spec.mask.args` pass: `mask_args_json.go:78-91`. Ran `prioritty` (rejected) and `shwLast` (accepted) |
| 26 | 158-159 | Field list is in reference/policy-schema.md | duplicate | Owner: reference/policy-schema.md |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 34 | yaml | `SqlAccessPolicy` anatomy | untested | verified by hand: `validate --strict` OK |
| 76 | yaml | Three masks, one per `enforcementMode` | untested | verified by hand: validate OK. Runner: `Enforce` masks, the other two are shadow only |
| 114 | yaml | Minimum set of three objects | untested | verified by hand: validate OK. Not served |
| 152 | console | `policy validate` output | untested | verified by hand: line format matches. Digest is made up |

## docs/policies/matching.md

Prose words now: 791. Suggested after rewrite: 450.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-8 | Selector has `any` (OR) or `all` (AND). A clause has optional `subjects` and `resources`, both must hold | implemented (internal/policy/selector.go:290-311) | Clause `selector.go:390-422`. Ran |
| 2 | 8 | Empty selector matches nothing, the policy is inert | mismatch | Empty `spec.match` is a load error: `decoder.go:541-549`. Only an empty `exclude` matches nothing. Ran |
| 3 | 38-39 | Fields in one `subjects` block are ANDed. Empty block matches all, also anonymous | implemented (internal/policy/selector.go:424-482) | Ran |
| 4 | 43 | `groups`: at least one listed group | implemented (internal/policy/selector.go:431-435) | |
| 5 | 44 | `apiKeys`: API key whose id is listed | mismatch | Compares the subject id: `selector.go:436-440`. Ran live: key id `sl_demo_hello` no match, subject `hello` match |
| 6 | 45 | `roles`: `roles` claim, falls back to groups when the claim is absent | implemented (internal/policy/selector.go:441-456) | Ran three cases |
| 7 | 46 | `ipRanges`: remote IP inside a CIDR range | not implemented | Never matches on REST: address has a port. Ran live |
| 8 | 47-50 | `jwtClaims`: every check must pass, dotted path into nested claims | implemented (internal/policy/selector.go:484-542) | Ran nested path |
| 9 | 52-59 | Ops `Equals`, `NotEquals`, `In`, `NotIn`, `Exists`, `Matches` with `value`, `values`, `pattern` | implemented (pkg/apitypes/common.go:39-46) | Ran all six. Unknown `op` loads and never matches |
| 10 | 61-63 | Claim comparison is type-strict | implemented (internal/policy/selector.go:651-661) | Tested: `TestDeepEqualNumericStrictTyping`. Ran |
| 11 | 67-69 | Table key is `catalog.schema.table`, seen in audit records and test assertions | implemented (internal/policy/conflict.go:405-407) | Lower case. Unqualified table is `..orders` (ran) |
| 12 | 73-78 | Resource fields `catalogs`, `schemas`, `tables`, `columns`, `tags`, `actions` | implemented (pkg/apitypes/common.go:51-58) | Compile `selector.go:173-222` |
| 13 | 76 | `columns` is required for masks and does not limit table matching | implemented (internal/policy/selector.go:333-365) | |
| 14 | 78 | `actions`: `SELECT`, `INSERT`, `UPDATE`, `DELETE` | duplicate | Owner: access-control.md |
| 15 | 80-85 | Dotted pattern is a load error, `\.` escapes a dot, unqualified table has empty catalog and schema | implemented (internal/policy/selector.go:176-186) | Tested: `TestCompile_RejectsDottedNamePatterns`. Ran all three |
| 16 | 87-90 | Clause matches when one table matches. Per-table kinds check each table alone | implemented (internal/policy/selector.go:316-328) | |
| 17 | 94-100 | Patterns are anchored. `*` in one segment, `**` across segments, `\*` literal | implemented (pkg/apitypes/wildcard.go:31-85) | Ran. `**` only differs from `*` for a name with a dot. Names match without regard to case (`selector.go:187-197`) |
| 18 | 104-106 | Same kind, same target: priority, then specificity, then name. Specificity counts non-wildcard patterns | mismatch | Specificity is for masks only: `conflict.go:295-303`. Count is catalogs, schemas, tables, columns and tags, best clause wins: `selector.go:216-217`, `370-386` |
| 19 | 128-130 | Example: `customers.email` nulled, other `email` hashed, priority 60 wins | implemented (internal/policy/conflict.go:308-333) | Ran in the runner |
| 20 | 136-137 | `exclude` on reject, rewrite, approval drops the whole policy | implemented (internal/policy/engine.go:178-188) | |
| 21 | 138-142 | `SqlAccessPolicy` `exclude` works per table | implemented (internal/policy/conflict.go:175-188) | Tested: `TestEngine_AccessExcludeIsPerTable`. Ran |
| 22 | 143-144 | Row filter and mask `exclude` works per table | implemented (internal/policy/conflict.go:217-225) | Tested: `TestExclude_ResourceCarveOutIsPerTable` |
| 23 | 148-150 | `combine`: `restrictive` ANDs (default), `permissive` ORs, values bind as `$N` | duplicate | Owner: row-filters.md |
| 24 | 163 | Exclude a group (break glass) | implemented (internal/policy/conflict.go:284-286) | Tested: `TestExclude_SubjectBreakGlassStillWorks`. Ran |
| 25 | 179-180 | Catalog match needs qualified table names | implemented (internal/policy/selector.go:551-553) | Ran |
| 26 | 194-195 | Match by tag. Unknown tag is a compile error | implemented (internal/policy/selector.go:209-215) | Tested: `TestTags_UnknownTagFailsCompile`. Ran |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 10 | yaml | Allow with two `any` clauses | untested | verified by hand: validate OK |
| 28 | yaml | Fragment: `all` with `groups` and `ipRanges` | broken | Loads when wrapped. `ipRanges` never matches on a live server |
| 108 | yaml | Two overlapping masks | untested | verified by hand: validate and runner. `tables: ["*"]` makes every table need a schema entry |
| 152 | sql | Combine, before and after | untested | verified by hand: runner gives the same text for both forms |
| 165 | yaml | Mask except `sre` | untested | verified by hand: validate and runner |
| 182 | yaml | Allow a whole catalog | untested | verified by hand: validate OK |
| 197 | yaml | `DataClassification` and tag mask | untested | verified by hand: validate and runner (`email` and `phone` get `hash`) |

## docs/policies/conditions.md

Prose words now: 453. Suggested after rewrite: 220.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5 | CEL is used in three places | implemented (internal/policy/cel.go:30-53) | Conditions, reject rules, row filter expression |
| 2 | 7-10 | `spec.conditions`: each must type-check as bool at load, all must be true, a run-time error denies the request | implemented (internal/policy/engine.go:112-126) | Compile `cel.go:70-107`. Ran live: 403 `ACL_DENIED`, "policy condition evaluation failed". A bare `query.has_cte` is `dyn` and fails to load |
| 3 | 11-13 | Reject rule: empty expression always fires, an error denies | duplicate | Owner: guardrails.md. Code `engine.go:242-269` |
| 4 | 14-15 | `filter.expression` is lowered to SQL at compile time | duplicate | Owner: row-filters.md |
| 5 | 19-28 | Variables `subject.*` (6), `request.*` (3), `query.*` (14) | implemented (internal/policy/cel.go:118-175) | All 23 names exist. No doc name is missing in code, no code name is missing in the doc |
| 6 | 23-25 | `request.remote_ip`, `request.user_agent`, `request.headers` | not implemented | Never filled on a live server. Ran live: all three empty |
| 7 | 30 | `query.tables` holds `catalog.schema.table` keys | implemented (internal/policy/cel.go:155-158) | Lower case |
| 8 | 30-31 | `subject.auth_method` is `jwt`, `api_key`, `admin_token` or `none` | mismatch | Only `jwt` and `api_key` are set. `admin_token`, `none` are declared only: `internal/identity/userctx.go:20-21`. Ran live: `api_key` |
| 9 | 33-36 | There is no `now` variable | implemented (internal/policy/cel.go:32-36) | Ran: load error. The cache reason is internal |
| 10 | 38-42 | A missing map key is a run-time error. Guard with `'k' in subject.claims` | implemented (internal/policy/engine.go:112-126) | Ran live: "no such key" |
| 11 | 46-48 | `filter.expression` sees `subject`, `request`, `row` | duplicate | Owner: row-filters.md |
| 12 | 50-55 | Allowed forms of the row filter subset | duplicate | Owner: row-filters.md |
| 13 | 57-59 | Rejected at load: arithmetic, function calls, macros, `query.*`, dynamic string arguments | duplicate | Owner: row-filters.md |
| 14 | 106-109 | Prefer `subjects.ipRanges` for network checks | not implemented | `ipRanges` never matches, `request.remote_ip` is empty |
| 15 | 113-114 | Predicate form and CEL form give the same SQL | duplicate | Owner: row-filters.md. Ran: same text |
| 16 | 143 | A filter sets exactly one of `predicate`, `expression` | duplicate | Owner: row-filters.md |
| 17 | 143-145 | Without the claim the template cannot render and the request is denied | mismatch | HTTP 500 `ERR_INTERNAL`: `inject_filter.go:297`, `rewriter/errors.go:77`. Ran live |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 63 | yaml | Fragment: four conditions | untested | verified by hand: loads when wrapped |
| 85 | yaml | `QueryRejectPolicy` with two rules | broken | Loads. Rule `internal-network-only` rejects every query: `request.remote_ip` is always empty |
| 116 | yaml | Tenant filter, predicate and CEL | untested | verified by hand: validate and runner |

## docs/policies/testing.md

Prose words now: 618. Suggested after rewrite: 500.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-6 | `sluice policy test <policy-dir>` runs suites, each case is an identity, a SQL statement and asserts | implemented (cmd/sluice/policytest.go:24-103) | Ran |
| 2 | 6-8 | Runs the same parse, evaluate, rewrite steps as the server, so a passing suite reflects real enforcement | mismatch | Steps are the same (`internal/policytest/policytest.go:260-299`). The conclusion is not: see "Runner reference" below |
| 3 | 12 | Suites default to `<policy-dir>/tests/*.yaml`, `--tests <file-or-dir>` overrides | implemented (cmd/sluice/policytest.go:66-80) | Also `*.yml`. Not recursive: `policytest.go:196-220` |
| 4 | 13-14 | Loader skips `tests`, `testdata`, dot directories | duplicate | Owner: index.md |
| 5 | 26-27 | Suite is plain YAML with `cases`, no `apiVersion`. Every `expect` field is optional | implemented (internal/policytest/policytest.go:38-79) | |
| 6 | 33-37 | `identity`: `subject` (omit for anonymous), `issuer`, `email`, `groups`, `claims` | implemented (internal/policytest/policytest.go:44-50) | Without `subject` the groups and claims are dropped: `policytest.go:252-254` |
| 7 | 38-40 | `request`: `headers`, `userAgent` | implemented (internal/policytest/policytest.go:53-56) | Runner only. The server never fills them |
| 8 | 43 | `expect.outcome`: `allow`, `deny`, `reject` | implemented (internal/policytest/policytest.go:273-275) | |
| 9 | 44 | `filters`: table keys | implemented (internal/policytest/policytest.go:349-355) | |
| 10 | 45 | `masks`: `table.column=type` | implemented (internal/policytest/policytest.go:357-363) | Lists every mask on the tables read, SQL and post-query, also for columns not selected |
| 11 | 46 | `postMasks`: same format | implemented (internal/policytest/policytest.go:373-379) | Only post-query masked columns in the result |
| 12 | 47-49 | `applied`: `Kind/Name` of every enforcing policy | implemented (internal/policytest/policytest.go:365-371) | |
| 13 | 50-51 | `rewrittenSqlContains`, `rewrittenSql` | implemented (internal/policytest/policytest.go:316-323) | |
| 14 | 52 | `denyPolicy` | implemented (internal/policytest/policytest.go:276-284) | |
| 15 | 53 | `errorCode`: expected rewrite error | implemented (internal/policytest/policytest.go:299-314) | Also `ACL_DENIED` on a deny and the first rule code on a reject |
| 16 | 54 | `rejections`: `policy/rule` | implemented (internal/policytest/policytest.go:341-347) | |
| 17 | 59-60 | Lists compare as sorted sets and must be equal | implemented (internal/policytest/policytest.go:328-339) | |
| 18 | 61 | SQL asserts ignore whitespace differences | implemented (internal/policytest/policytest.go:381) | |
| 19 | 62-65 | `errorCode` always runs the rewrite and fails when it succeeds. An allow case without it fails when the rewrite fails | implemented (internal/policytest/policytest.go:292-314) | Tested: `TestRunner_ErrorCodeChecked`, `TestRunner_AllowCaseFailsOnRewriteError` |
| 20 | 69-72 | A masked table needs a `schema:` entry, else a rewrite error that asks for the block | implemented (internal/policytest/policytest.go:138-161) | Ran: code `ERR_REWRITE_FAILED` |
| 21 | 72-76 | Filter-only table needs no entry, except under an outermost `SELECT *` on a post-query mask query. Star over subquery, CTE or function fails the same way | implemented (internal/rewriter/expand_star.go:27) | Ran four cases: `ERR_MASK_UNSUPPORTED_CONTEXT` |
| 22 | 92-95 | Keys are three-part names, columns in table order, static data | implemented (internal/policytest/policytest.go:143-147) | Bad key: exit 1 |
| 23 | 99 | A failing case prints its diff | implemented (cmd/sluice/policytest.go:105-117) | Ran |
| 24 | 100-101 | Flags `--tests`, `--strict`, `--json` | implemented (cmd/sluice/policytest.go:99-101) | `--strict` covers policies only |
| 25 | 105-107 | Exit 0 pass, 1 I/O or flag error, 3 compile failure, case failure or zero cases | mismatch | Compile failure is exit 1: `cmd/sluice/policytest.go:53-63`. Ran. The rest is right |
| 26 | 109-110 | Zero cases exits 3 on purpose | implemented (cmd/sluice/policytest.go:92-94) | Ran |
| 27 | 114-117 | `examples/policies/` with `basic.yaml` and `select-star.yaml` | implemented (examples/policies/allow.yaml:1) | Ran: 4 passed |
| 28 | 158 | Default deny needs no deny policy | duplicate | Owner: access-control.md |
| 29 | 162-163 | Built from source with CGO, needs a C toolchain | duplicate | Owner: install page. Runner refuses the `pure_parser` build: `policytest.go:96-98` |
| 30 | 181-199 | `sluice policy explain` flags, output, `--json` with `shadow` | duplicate | Owner: reference/cli.md. Code `cmd/sluice/policy.go:89-237`. Ran |

### Runner reference (for the example harness)

Suite file (`internal/policytest/policytest.go:38-79`). Parsed without strict mode: unknown keys are ignored, also with `--strict` (ran `outcom:`, the case passes).

| Key | Type | Meaning |
|---|---|---|
| `schema` | map of `catalog.schema.table` to column list | Static columns for masked tables. Key must have three parts |
| `cases[].name` | string | Printed in the report |
| `cases[].identity.subject` | string | Empty means anonymous (no user at all) |
| `cases[].identity.issuer`, `.email` | string | |
| `cases[].identity.groups` | list | |
| `cases[].identity.claims` | map, may nest | |
| `cases[].request.headers` | map | |
| `cases[].request.userAgent` | string | |
| `cases[].sql` | string | One statement |
| `cases[].expect` | map | Ten fields below, all optional |

`expect` fields, all ten:

| Field | Compared with | Notes |
|---|---|---|
| `outcome` | `allow`, `deny`, `reject` | |
| `denyPolicy` | Name of the deny policy | Empty for default deny |
| `errorCode` | Code of the rewrite error | Forces a rewrite. `ACL_DENIED` for any deny, also when the policy sets its own `errorCode`. First rule code for a reject |
| `rejections` | Set of `policy/rule` | |
| `filters` | Set of lower case table keys | Unqualified table: `..orders` |
| `masks` | Set of `table.column=type` | All masks on the tables read. Column is the selector pattern as written |
| `postMasks` | Set of `table.column=type` | Needs a rewrite that succeeds |
| `applied` | Set of `Kind/Name` | Enforcing policies only, no shadow |
| `rewrittenSqlContains` | Substrings | Whitespace ignored |
| `rewrittenSql` | Whole text | Whitespace ignored |

Exit codes as run:

| Code | When |
|---|---|
| 0 | All cases pass |
| 1 | Flag error, missing tests path, suite YAML error, bad `schema` key, policy compile error (for example unknown tag, `mask.type: custom`) |
| 3 | Policy decode or validation error, a failing case, zero cases |

Text output: `PASS  <name>` or `FAIL  <name>`, failures indented by 8 spaces, then an empty line and `N passed, N failed, N total`.

`--json` shape (indent 2, `cmd/sluice/schema.go:97-104`):

```json
{"total": 1, "passed": 0, "failed": 1,
 "cases": [{"name": "wrong outcome", "passed": false, "failures": ["outcome: want \"deny\" got \"allow\""]}]}
```

`failures` is left out for a passing case.

What the runner does not check:

- No SQL is executed. No rows, no values, no bound parameter values. SQL that DuckDB refuses at run time passes (`IsNull` filters, a filter column that does not exist, type errors).
- Post-query masks (`hmac_sha256`, `fpe`, `jitter`, `fake`) are planned, not applied. Their keys are not read.
- A SQL mask with `saltRef` needs the secret at test time, else the case fails with a rewrite error. Ran.
- No live datasource: masked tables need the `schema:` block.
- No auth method and no remote address. `subjects.apiKeys` and `subjects.ipRanges` cannot match. `subject.auth_method` is `""`. `request.remote_ip` is empty.
- Only the YAML engine runs (`policytest.go:106`). `RelationshipPolicy` (OpenFGA) and OPA are not evaluated.
- Server gates before policy are not run: two-part name refusal, cross-catalog gate, SQL size, rate limit, budget, approval hold, rewrite cache (`internal/queryservice/execute.go:127-175`). The statement kind gate and the function gate do run, inside the rewrite.
- No assert for: shadow matches, approval requirement, deny message, rejection message, a custom deny `errorCode`, the timeout of a `QueryRewritePolicy`.
- SQL that pg_query cannot parse: no table list, so an expected `deny` can pass for the wrong reason. `errorCode` cannot assert it ("rewrite requested but SQL did not parse").
- One policy directory per run. No setup step, no per-case policies.

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 16 | text | Directory layout | untested | verified by hand |
| 29 | yaml | Suite format, one case with all fields | broken | The case cannot pass (see Summary). Field names are right |
| 78 | yaml | Schema fixture suite | untested | verified by hand: PASS with matching policies |
| 119 | yaml | `basic.yaml` | untested | verified by hand: same text as `examples/policies/tests/basic.yaml` without comments, 3 PASS. No test or CI job runs it |
| 148 | console | `policy test examples/policies` output | untested | verified by hand: identical |
| 165 | yaml | GitHub Actions steps | untested | not verified: needs CI. `make build` writes `./bin/sluice` (`Makefile:8-9`), Go 1.26 matches `go.mod` |
| 184 | console | `policy explain` output | untested | verified by hand: identical |

## docs/policies/access-control.md

Prose words now: 437. Suggested after rewrite: 250.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-6 | `spec.effect` is `allow` or `deny`, lower case | implemented (pkg/apitypes/decoder.go:264-272) | Ran `Allow`: load error |
| 2 | 8-10 | Default deny, also with an empty directory. Cannot be configured away | mismatch | True for the YAML engine (`conflict.go:145-153`). `policies.engine: opa` or `composite` changes it: `cmd/sluice/policyengine.go:23-46`. Read only |
| 3 | 11-13 | Deny wins. Highest priority deny gives `message` and `errorCode`, defaults `access denied` and `ACL_DENIED` | implemented (internal/policy/conflict.go:121-144) | Tested: `TestEngine_DenyOverridesAllow`. Ran live: custom code, HTTP 403 |
| 4 | 30-31 | An explicit deny stops evaluation of filters, masks, rewrites, approvals | implemented (internal/policy/conflict.go:51-57) | |
| 5 | 33-38 | An allow covers only the tables it names. Uncovered table: `no SqlAccessPolicy allows <table>`. A deny on any table blocks the query | implemented (internal/policy/conflict.go:155-169) | Tested: `TestEngine_AllowDoesNotCoverOtherTables`. Ran live |
| 6 | 42-44 | `resources.actions`: `SELECT`, `INSERT`, `UPDATE`, `DELETE`, upper case. Unknown verb never matches | implemented (internal/policy/selector.go:402-409) | Tested: `TestActionScoping`. Values are not validated. `EXPLAIN SELECT` counts as `SELECT`, `SET`, `SHOW`, `PRAGMA` have no action (`engine.go:275-293`) |
| 7 | 46-51 | Read-only floor: `SELECT`, `EXPLAIN`, `SET`, `SHOW`, `PRAGMA` pass. Writes: `ACL_REJECTED` "write operations are not permitted". DDL, `COPY`, `ATTACH`: `ERR_UNSUPPORTED_SYNTAX` | implemented (internal/rewriter/rewriter.go:353-369) | Kinds `internal/parser/shape.go:29-36`. Ran live: writes 403, `COPY` 400. Without a matching allow the answer is `ACL_DENIED` first |
| 8 | 55-58 | `conditions` and `enforcementMode` apply | duplicate | Owners: conditions.md, index.md |
| 9 | 62-66 | Order: deny, reject, approval. Then filters, masks, rewrites shape the query | implemented (internal/policy/conflict.go:51-95) | Approval `engine.go:141-148`. Ran: deny beats reject |
| 10 | 88-89 | A clause with only `resources` matches every subject | implemented (internal/policy/selector.go:391-395) | Ran |
| 11 | 107-108 | The key id comes from `SubjectBinding` `apiKeys[].id` | mismatch | The selector compares the subject id. See matching.md claim 5 |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 15 | yaml | `deny-salaries` | untested | verified by hand: validate and runner (`denyPolicy`, message) |
| 72 | yaml | Allow analysts on one schema | untested | verified by hand: validate OK |
| 91 | yaml | Deny `raw_events` | untested | verified by hand: validate OK |
| 110 | yaml | Allow `SELECT` for one API key | untested | verified by hand: validate OK. Matches only when the binding sets no `claims.subjectId` |

## docs/policies/row-filters.md

Prose words now: 1537. Suggested after rewrite: 650.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-7 | Each matched table is wrapped in a subquery with a WHERE clause. Values bind as `$N` | implemented (internal/rewriter/inject_filter.go:256-319) | Ran |
| 2 | 16-19 | The wrap covers joins, FROM subqueries, CTEs, set operations and subqueries in expressions | implemented (internal/rewriter/inject_filter.go:174-252) | Tested: `internal/queryservice/rewrite_coverage_duckdb_test.go`. Ran one statement with all forms |
| 3 | 19 | Table names match without regard to case, also quoted | implemented (internal/rewriter/alias_map.go:58-64) | Ran live |
| 4 | 19-22 | Two-part `shop.orders` is refused with `ACL_REJECTED` | implemented (internal/queryservice/execute.go:164-175) | Ran live: 403. Only when the first part is an attached catalog. Not checked by `policy test` |
| 5 | 23-24 | Alias is kept. Without alias the wrapper has the table name | internal detail | `inject_filter.go:267-279` |
| 6 | 24-26 | Table named like a DuckDB keyword, no alias: `ERR_UNSUPPORTED_SYNTAX` | internal detail | User effect: 400, "give table X an alias". `inject_filter.go:274-277`. Ran |
| 7 | 27-29 | `sluice_src` is reserved: `ERR_UNSUPPORTED_SYNTAX` | internal detail | User effect: 400 on any statement that contains the text. `inject_filter.go:49-67`. Ran live |
| 8 | 29-30 | Filter column must exist in every matched table, not checked | implemented (internal/rewriter/inject_filter.go:406-417) | Ran live: 400 `ERR_QUERY_FAILED` at query time |
| 9 | 31-32 | With a mask the same subquery lists the columns | duplicate | Owner: column-masks.md |
| 10 | 34-35 | Exactly one of `filter.predicate`, `filter.expression` | implemented (pkg/apitypes/decoder.go:278-287) | Ran |
| 11 | 39 | Leaf is `column`, `op`, value set. Ops are PascalCase | implemented (internal/policy/predicate.go:105-170) | Ran `equals`: "unknown op" |
| 12 | 43-45 | `Equals`, `NotEquals`, `GreaterThan`, `GreaterThanOrEqual`, `LessThan`, `LessThanOrEqual` | implemented (internal/rewriter/inject_filter.go:456-467) | Ran live |
| 13 | 46 | `Between`, exactly 2 values | implemented (internal/rewriter/inject_filter.go:476-477) | Ran live |
| 14 | 47 | `In`, `NotIn`, 1 or more values | implemented (internal/rewriter/inject_filter.go:472-475) | Ran live |
| 15 | 48 | `Like`, `NotLike` as `~~`, `!~~` | implemented (internal/rewriter/inject_filter.go:468-471) | Ran live |
| 16 | 49 | `IsNull`, `IsNotNull` as `col IS NULL`, `col IS NOT NULL` | mismatch | Emits `col OPERATOR(IS) NULL`: `inject_filter.go:419-434`. DuckDB parser error, HTTP 500 `ERR_INTERNAL`. Ran live |
| 17 | 50-51 | `StartsWith`, `EndsWith`, `Contains` as `starts_with`, `ends_with`, `contains` | implemented (internal/rewriter/inject_filter.go:478-483) | Ran live |
| 18 | 52 | `Matches` as `regexp_matches` | implemented (internal/rewriter/inject_filter.go:484-488) | Ran live |
| 19 | 54-57 | The three string ops compare literally. `Matches` is a partial match | implemented (internal/rewriter/inject_filter.go:493-498) | Ran live: `StartsWith "a%"` gives no rows, `Matches "ob@"` hits |
| 20 | 59 | Nodes nest with `all`, `any`, `not` | implemented (internal/policy/predicate.go:75-103) | Ran |
| 21 | 85 | The example injects `WHERE deleted_at IS NULL AND (...)` | mismatch | Real: `WHERE sluice_src.deleted_at OPERATOR(IS) NULL AND (sluice_src.region IN ($1, $2) OR sluice_src.vip = $3) AND NOT sluice_src.status = $4` |
| 22 | 93 | `{{ subject.sub }}` | implemented (internal/policy/template.go:98-102) | |
| 23 | 94 | `{{ subject.issuer }}`, `{{ subject.email }}` | implemented (internal/policy/template.go:103-106) | Unset gives `""`, the query runs. Ran live |
| 24 | 94 | `{{ subject.groups }}` | mismatch | Gives a list, DuckDB cannot bind it: HTTP 500 `ERR_INTERNAL`. Ran live |
| 25 | 95 | `{{ subject.auth_method }}`, `{{ subject.request_id }}` | implemented (internal/policy/template.go:109-112) | Read only |
| 26 | 96-97 | `{{ subject.jwt.<path> }}`, `{{ subject.claims.<path> }}`, `{{ subject.<name> }}` falls to the claim | implemented (internal/policy/template.go:113-117) | Ran live (`subject.tenantId`) |
| 27 | 98 | `{{ request.remote_ip }}`, `{{ request.user_agent }}`, `{{ request.headers.<name> }}` | not implemented | No request facts on a live server: HTTP 500 `ERR_INTERNAL` "request facts unavailable". Ran live |
| 28 | 100-101 | A template spans the whole value. Mixed text is a load error | implemented (internal/policy/template.go:41-44) | Ran |
| 29 | 101-103 | Missing variable: the request is denied | mismatch | HTTP 500 `ERR_INTERNAL`, not a deny. Ran live |
| 30 | 107-108 | `filter.expression` is CEL over `row`, `subject`, `request` | implemented (internal/policy/cel_sql.go:30-43) | Ran live |
| 31 | 125-131 | Subset: comparisons, `&&`, `\|\|`, `!`, `in [list]`, `startsWith`, `endsWith`, `contains`. The rest fails at load | implemented (internal/policy/cel_sql.go:45-213) | Tested: `TestCELRowFilterToPredicate`, `TestCELRowFilterRejectsOutOfSubset`. Operands can be swapped |
| 32 | 135-137 | Filters fold in policy order. Each next filter uses its own `combine`, default `restrictive` | implemented (internal/policy/conflict.go:211-265) | Ran. `combine` is not validated |
| 33 | 137-138 | "the tenant filter from the policy model (index.md)" | mismatch | index.md has no row filter |
| 34 | 148 | Use `permissive` only for carve-outs | fluff | |
| 35 | 152-155 | On a rewritten statement DuckDB parses the caller text and the rewritten text first | internal detail | `internal/rewriter/dialect.go:322-336` |
| 36 | 157-159 | Refusal codes `ERR_UNSUPPORTED_SYNTAX` and `ERR_SYNTAX` with their messages | implemented (internal/rewriter/dialect.go:331-334) | Ran |
| 37 | 161-163 | A limit or sample on every query checks every `SELECT`. An untouched query is not checked | implemented (internal/rewriter/rewriter.go:342-347) | Ran |
| 38 | 165-181 | Table of 15 refused forms with fixes | internal detail | All 15 rows and fixes ran in the runner: codes match. Tested: `internal/rewriter/dialect_test.go`. User line: DuckDB-only syntax can be refused on filtered, masked or limited queries, 400 `ERR_UNSUPPORTED_SYNTAX` or `ERR_SYNTAX` |
| 39 | 183-184 | Some refused forms would give the same rows | internal detail | |
| 40 | 186-189 | Forms that are rewritten and work (`LIKE ESCAPE`, `INTERVAL` units, casts, `FLOAT`, `EXTRACT`) | internal detail | Ran five samples |
| 41 | 190-197 | Alias spelling: keyword, type or unit names come back lower case | internal detail | User effect: quote such an alias. Ran live: `AS Month` gives `month`, `AS MyId` stays |
| 42 | 201-204 | `DryRun` rollout, `shadow` in explain | duplicate | Owner: index.md. Admin route `internal/transport/admin/routes.go:20` |
| 43 | 208, 241-242 | Recipes: tenant by `{{ subject.tenantId }}`, region by claim | implemented (internal/policy/template.go:115-117) | Ran live |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 9 | sql | Client SQL and executed SQL | untested | verified by hand: runner gives the same text |
| 61 | yaml | `filter-active-eu`, nested predicate | broken | Loads. Uses `IsNull`: 500 on every query. Ran live |
| 110 | yaml | Fragment: predicate form | untested | verified by hand: loads when wrapped |
| 119 | yaml | Fragment: CEL form | untested | verified by hand: loads when wrapped |
| 141 | sql | Combined WHERE, both modes | broken | Shape differs from a real run (see Summary) |
| 210 | yaml | `filter-tenant` | untested | verified by hand: validate OK, same policy ran live |
| 226 | yaml | `hide-soft-deleted` | broken | Loads. Uses `IsNull` |
| 244 | yaml | `filter-region`, CEL | untested | verified by hand: validate OK |

## docs/policies/column-masks.md

Prose words now: 2215. Suggested after rewrite: 800.

Mask types against `pkg/mask/registry.go:85-96`: nine registered (`null`, `constant`, `partial`, `hash`, `regex`, `truncate`, `fpe`, `jitter`, `fake`). The doc names the same nine. `custom` and `external` are not registered and the doc says so.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-6 | Masks run at one of two stages | implemented (pkg/mask/rowmask.go:56-65) | |
| 2 | 10 | SQL stage: `null`, `constant`, `partial`, `hash` (sha256), `regex`, `truncate`, applied in the table subquery | implemented (internal/rewriter/inject_filter.go:325-348) | Ran live, all six |
| 3 | 11 | Post-query: `hash` (hmac_sha256), `fpe`, `jitter`, `fake`, applied in Go per row | implemented (internal/queryservice/maskedrows.go:48-84) | Ran live, all four |
| 4 | 13-14 | A masked table is read through a subquery that lists its columns | implemented (internal/rewriter/inject_filter.go:302-308) | Ran |
| 5 | 26-27 | No `resources.columns` means no column is masked | implemented (internal/policy/selector.go:349-354) | Ran: loads, masks nothing. A `tags` selector takes columns from the classification (`conflict.go:390-392`) |
| 6 | 28-32 | Mask follows the column name without regard to case. Every use sees the masked value | implemented (internal/rewriter/substitute_mask.go:631-645) | Ran live: `"EMAIL"`, `SELECT c`, `WHERE email LIKE`. The list of clauses is internal detail |
| 7 | 33-36 | Masked table needs a schema cache entry: `ERR_REWRITE_FAILED`. Schema not loadable or catalog not attached: `ERR_DATASOURCE_UNAVAILABLE` | implemented (internal/rewriter/errors.go:72-75) | First code ran in the runner. Second read only |
| 8 | 37-44 | Schema cache rules: reload empties it, catalog loads on a miss, no timer, `cache.rewrite.ttl` | internal detail | User line: add the mask before the column. Read only: `internal/schema/cache.go:162-181`, `215`, `cmd/sluice/bootstrap.go:499`, `internal/config/server.go:478-480` |
| 9 | 45-47 | A rule with columns and no tables applies to every table. Each needs a schema entry | implemented (internal/rewriter/substitute_mask.go:504-517) | Ran in the runner with `tables: ["*"]` |
| 10 | 48-49 | A row filter tests the raw value, the query sees the masked value | implemented (internal/rewriter/inject_filter.go:293-308) | Seen in the rewritten SQL |
| 11 | 50-51 | Masked column without alias keeps the source column name | implemented (internal/rewriter/inject_filter.go:342) | Ran live |
| 12 | 52-57 | Type is the mask output type. `constant` text on a date column fails comparisons: `ERR_QUERY_FAILED` | implemented (internal/rewriter/substitute_mask.go:698-701) | Ran live: 400. "NULL reads as INTEGER" not run |
| 13 | 58-60 | `rowid` fails on a masked table. Schema-qualified column: `ERR_UNSUPPORTED_SYNTAX` | internal detail | Ran live: `rowid` 400 `ERR_QUERY_FAILED`, `main.m1.email` 400 `ERR_UNSUPPORTED_SYNTAX` (`inject_filter.go:96-126`) |
| 14 | 61-63 | `sluice_src` is reserved | duplicate | Owner: row-filters.md |
| 15 | 64-74 | Post-query masked column only as a bare item of the outermost select list, else `ERR_MASK_UNSUPPORTED_CONTEXT`. Outer `SELECT *` is expanded from the schema cache | implemented (internal/rewriter/substitute_mask.go:352) | Star `expand_star.go:27`. Ran 12 forms in the runner, 5 live (HTTP 400). Keep one line |
| 16 | 75-90 | Refused: alias reuse, position in `ORDER BY` / `GROUP BY`, `SELECT DISTINCT`, `unnest`, `USING` / `NATURAL` joins. Result shape is checked | internal detail | Ran 12 forms: all `ERR_MASK_UNSUPPORTED_CONTEXT`. Shape check `maskedrows.go:90-107`, read only |
| 17 | 91-101 | Refused: table alias as value, schema prefix, dotted field, `LATERAL` star | internal detail | Ran 8 forms: as documented, the last one `ERR_UNSUPPORTED_SYNTAX` |
| 18 | 102-106 | Bare `CURRENT_DATE` and similar are refused when the name is masked | internal detail | Ran 2 forms |
| 19 | 107-124 | Refused on every table of a post-query mask query: column alias list, join alias, `#N`, `alias.` prefix, unknown qualifier, star over `USING` | internal detail | Ran 12 forms: as documented |
| 20 | 125-134 | Name binding refusals in subqueries and join conditions | internal detail | Ran 3 forms |
| 21 | 135-138 | DuckDB forms refused, see row-filters.md | duplicate | Owner: row-filters.md |
| 22 | 139-141 | One winner per column: priority, specificity, name. Masks never stack | implemented (internal/policy/conflict.go:295-333) | Second pass per real column `substitute_mask.go:647-667`. Ran |
| 23 | 143-144 | Args are validated at load and again at rewrite | mismatch | Once, at load: `snapshot.go:521-533`. Bad args do fail `policy validate`. Unknown arg keys are accepted |
| 24 | 146-149 | `custom` and `external` parse, compile fails with "provider not enabled" | implemented (internal/policy/snapshot.go:517-520) | Ran: exit 3. Not registered in `registry.go:85-96` |
| 25 | 153 | `null`: SQL `NULL`, no args | implemented (pkg/mask/null.go:15-17) | Ran live |
| 26 | 170 | `constant`: `value` required, scalar, written as a SQL constant | implemented (pkg/mask/constant.go:30-39) | Required: `snapshot.go:513-516`. Ran live |
| 27 | 185-194 | `partial`: `showFirst`, `showLast` default 0, `maskChar` default `*`, one must be above 0, `NULL` passes | implemented (pkg/mask/partial.go:23-57) | Rule `decoder.go:303-309`. Ran live |
| 28 | 209-215 | `hash`: `algorithm` `sha256` (default) or `hmac_sha256`. `saltRef` needed for HMAC, optional salt for sha256 | implemented (pkg/mask/hash.go:25-69) | Ran live: plain, salted, HMAC |
| 29 | 226-227 | Example hash `ff8d9819fc0e12bf…` | implemented (pkg/mask/hash.go:30) | Equals sha256 of the input (computed) |
| 30 | 231-237 | `regex`: `regexp_replace(col, pattern, replacement, 'g')`, RE2, at most 512 bytes, empty replacement allowed | implemented (pkg/mask/regexmask.go:26-52) | Ran live |
| 31 | 252-257 | `truncate`: `length` at least 1, optional `suffix` | implemented (pkg/mask/truncate.go:17-38) | Ran live |
| 32 | 272-281 | `fpe`: FF1, other characters pass, `keyRef`, `tweak` hex up to 16 bytes, `alphabet` `numeric` (default), `lower`, `alphanumeric`, `customAlphabet` 2 to 36 | implemented (pkg/mask/fpe.go:45-123) | Ran live: `numeric`, `lower` |
| 33 | 297-305 | `jitter`: value times (1 + f), same input gives same output, non-numeric aborts, `range` in (0, 1), `seed` | implemented (pkg/mask/jitter.go:32-93) | Ran live: 72000 gives 69819 twice. Abort is HTTP 200 with a cut body. Only Go int, int32, int64, float32, float64 are handled (`jitter.go:69-82`), DECIMAL read only |
| 34 | 320-326 | `fake`: nine `fakeType` values, optional `seed`, same input gives same output | implemented (pkg/mask/fake.go:46-109) | Ran live (`name`) |
| 35 | 164, 181, 205, 248, 268 | Example outputs of `null`, `constant`, `partial`, `regex`, `truncate` | implemented (pkg/mask/partial.go:28-31) | All five reproduced live with the doc input |
| 36 | 292, 316, 337 | Example outputs of `fpe`, `jitter`, `fake` | fluff | Marked as illustrative. Real values depend on the key |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 16 | sql | Client SQL and executed SQL, null mask and filter | untested | verified by hand: runner gives the same text with fixture `[id, name, email, tenant_id, created_at]` |
| 155 | yaml | `null-ssn` | untested | verified by hand: validate OK, type ran live |
| 172 | yaml | `constant-email` | untested | verified by hand: validate OK, type ran live |
| 196 | yaml | `partial-email` | untested | verified by hand: validate OK, same args ran live |
| 217 | yaml | `hmac-email` | untested | verified by hand: validate OK, same args ran live. Secret not checked at validate |
| 239 | yaml | `regex-phone` | untested | verified by hand: validate OK, same args ran live |
| 259 | yaml | `truncate-notes` | untested | verified by hand: validate OK, same args ran live |
| 283 | yaml | `fpe-card` | untested | verified by hand: validate OK, same args ran live |
| 307 | yaml | `jitter-salary` | untested | verified by hand: validate OK, same args ran live |
| 328 | yaml | `fake-name` | untested | verified by hand: validate OK, same args ran live |
| 344 | yaml | PII bundle, four masks | untested | verified by hand: validate OK |

---

# Part B

Pages: guardrails, approvals, subjects, classification, rebac, opa.
Audited at commit 6f7b99f (HEAD ad57e1d adds docs files only) with `/tmp/sluice-bin/sluice`.

How things were checked:

- Every YAML policy block: `sluice policy validate --strict`. Every server config fragment: `sluice config validate --config`.
- Rewritten SQL and outcomes: `sluice policy test` suites in `/tmp/audit-policies-b/` (g1, g2, g3, c1, c2, c3) and `sluice policy explain`.
- Live runs: a scratch copy of `examples/hello-sluice` (SQLite seeded with python3), REST on 127.0.0.1:18160, admin on 18161. A python webhook receiver on 18162. A python fake of the OpenFGA `check` endpoint on 18163. Six server configs: yaml engine with approvals and budgets, composite `[yaml, rebac]`, `engine: opa`, composite `[yaml, opa]`, approvals with `persist: true`, and standalone `sluice mcp` over stdio.
- JWTs were HS256 and HS384 tokens made with python3. RS and ES keys, JWKS fetch and a real OpenFGA server were only read in code.
- "run" in a note means: observed in a live run or a CLI run. "read" means: code only.

## Summary

### Not implemented

- subjects.md:30 and :101-118: JWT mapping of `claims.tenantId`, `claims.allowedRegions` and extra keys. Only `subjectId`, `email` and `groups` are read for a JWT (internal/identity/jwt.go:265-291). Run: binding `tenantId: $.tenant`, token `tenant: acme`, filter `{{ subject.tenantId }}` gives 500 `ERR_INTERNAL`. It works only when the token has a claim that is named `tenantId` (template reads raw claims, internal/policy/template.go:115-118).
- subjects.md:31 and :59: "any extra key in the block becomes an additional named claim". `ClaimPaths.Extra` is tagged `json:"-"` (pkg/apitypes/subjectbinding.go:36) and the decoder works on JSON tags, so the map is never filled. Run: `sluice policy validate --strict` refuses an extra key ("unknown field"). Without `--strict` the key is dropped: API-key binding `team: Alice`, filter `{{ subject.team }}` gives 500 "claim path segment team missing".
- guardrails.md:17 and opa.md:54: `request.*` (remote IP, user agent, headers). The variables exist, but no transport fills `QueryRequest.Facts` (internal/transport/rest/query.go:48-57, internal/transport/mcp/tools.go:336-342). Run: a reject rule `request.user_agent != ''` never fires over REST, and the OPA `input.request` is always `{"remote_ip":"","user_agent":"","headers":{}}`. Only `sluice policy test` can set headers and user agent (internal/policytest/policytest.go:255-257).
- guardrails.md:78: `PRAGMA` as a statement that passes. No code produces `StmtPragma` (internal/pgquery/stmt.go:26-32). Run: `PRAGMA version` is a syntax error.
- guardrails.md:61: a selector-only veto by "an IP range". `subjects.ipRanges` parses `ip:port` with `net.ParseIP` (internal/policy/selector.go:461, value from internal/identity/apikey.go:177), so it never matches over REST. Run: `ipRanges: ["127.0.0.0/8"]` did not match a client on 127.0.0.1. Owner is matching.md (part A).
- rebac.md:86 already says `spec.backend.cacheTtl` is ignored. Confirmed by run (policy 60 s, server 3 s, refetch after 3 s). Drop the field from the page.
- subjects (type only, not in docs): `spec.groupsSource` exists in the type (pkg/apitypes/subjectbinding.go:22) and nothing reads it. Do not document it.

### Docs and code disagree

guardrails.md

- :16: doc "name identifies the rule in the error and the audit record". Code: the client error has `message`, `policy`, `code` only (internal/queryservice/execute.go:491-506). The audit record has the message and the policy name (internal/queryservice/audit_helpers.go:106-110). The rule name shows in `policy explain` (internal/policy/engine.go:331), in `policy test` (internal/policytest/policytest.go:344) and in the metric `sluice_policy_rejections_total{policy,rule}` (internal/policy/metrics.go:43-47). Run.
- :21-31: doc "the parsed statement's shape". Code: all flags except `tables` and `catalogs` describe the top-level SELECT only. For a set operation only the left arm is read, and a `WITH` on the set operation is not seen (internal/pgquery/shape.go:42-70). Run: the page's own rules `no-recursive-cte` and `no-star-joins` did not fire for a recursive CTE in front of a `UNION ALL`, a star join in a subquery, and a star join in the right arm of a union.
- :77: doc "a parameter limit is replaced by the cap". Code: any `$n` is refused with `ERR_UNSUPPORTED_SYNTAX` first (internal/rewriter/rewriter.go:175-177, internal/queryservice/execute.go:155). Run.
- :107: doc "On expiry the client receives ERR_TIMEOUT". True for normal values (run: 150 ms and 300 ms gave 504 `ERR_TIMEOUT`). With `timeout: "1ms"` 7 of 13 runs gave 500 `ERR_INTERNAL` ("INTERRUPT Error: Interrupted!"). The DuckDB interrupt error is not mapped (internal/executor/executor.go:167-180). Code bug, not a doc bug.

approvals.md

- :47-48: doc "reason is carried into the approval's status (GET /v1/approvals/{id} / check_approval)". Code: both return `approval_id`, `state`, `expires_at` only (internal/transport/rest/approvals.go:77-81, internal/transport/mcp/tools.go:95-99). Reasons are in the webhook and in `GET /admin/approvals`. Run.
- :51-54: doc "unparseable query with a non-empty when is parked". Code: the trigger is set (internal/policy/approval.go:51-53), but a parse error is returned before the approval gate (internal/queryservice/execute.go:236-245, gate at :251). Run: 400 `ERR_SYNTAX`, no webhook, nothing parked.
- :129: doc "Request or grant timed out: ERR_APPROVAL_EXPIRED, 410". Code: 410 only when a pending request expires while a client waits in `syncWait` (internal/queryservice/approval.go:97-102). An expired grant is dropped (internal/approval/broker.go:336-341) and the re-run parks again. Run: 202 with a new approval id and a new webhook.
- :128: doc "Approver rejected: ERR_APPROVAL_REJECTED, 403". Code: only a request that waits in `syncWait` gets it. Run: a re-run after the rejection gets 202 and a new approval. A polling client sees `state: rejected`.
- :160: doc "visible as a shadow match in sluice policy explain". Code: only with `--json` (`shadow` array). The text table does not print shadow entries (cmd/sluice/policy.go:197-218). Run.
- :132-133: doc "anything else is a 404". A request with no credential gets 401 (internal/transport/rest/routes.go:47). Run.

subjects.md

- :16: doc "validates iss, aud, exp, nbf and iat". Code: `iat` is not checked. The parser in use has no `WithIssuedAt` (internal/identity/jwt.go:195-200). The parser built at :109-113 is never used. Run: a token with `iat` one hour ahead got 200. Also: `aud` is checked only when `audience` is set (:224), and `exp` is required (:198).
- :65-67: doc "gives each subject a token bucket", "an API-key binding limits its one subject". Code: the spec is stored under `claims.subjectId` and under `spec.issuer` only (cmd/sluice/bootstrap.go:688-693). An API-key binding with neither has no limit. Run: key without `claims.subjectId`, `rps: 1, burst: 2`, 5 calls in a row passed. With `claims.subjectId` the third call got 429. The same gap holds for `budget` (cmd/sluice/budgetwire.go:70-75). Run: 15 rows served with `rowsPerDay: 5`.
- :67-68: doc "rps: 0 or no rateLimit block means unlimited". Code: then `limits.defaultSubjectRps` and `limits.defaultSubjectBurst` apply (internal/config/server.go:220-221, internal/ratelimit/limiter.go:112-114). Their default is 0.
- :68-69: doc "anonymous requests are bounded by limits.maxConcurrent". Code: REST refuses a request with no credential with 401 (internal/transport/rest/routes.go:28-31). Anonymous exists only on MCP with `mcp.allowAnonymous`.
- :70: doc "a changed spec resets the bucket". Code: every reload drops all buckets (internal/ratelimit/limiter.go:83-89).
- :75: doc "Usage (CPU milliseconds ...)". Code: the value is wall-clock time from request start until the executor returns (internal/queryservice/execute.go:421), not CPU time.
- :92: doc "failClosed: true, budget store unavailable => refuse queries". Code: `failClosed` only decides startup. The store cannot open: boot aborts. With `false`: budgets are off with an error log (cmd/sluice/budgetwire.go:19-49). At run time a failed flush is only logged (internal/budget/manager.go:274-292).
- :102: doc "a generous shared rate limit". Code: one bucket per subject under the issuer (internal/ratelimit/limiter.go:135-139, test `TestLimiter_PerIssuerAppliesPerSubject`).
- :13: doc "a duplicate issuer is a load error". True for `sluice serve` (internal/identity/binding.go:94-96, run: exit 1). `sluice policy validate --strict` does not catch it (run: exit 0).

rebac.md

- :62: doc "The only supported backend type is openfga". Code: `backend.type` is never read. `type: spicedb` or no type passes validation and is used as OpenFGA (pkg/apitypes/decoder.go:387-409, internal/rebac/engine.go:69-75). Run.
- :79-80 and :114-116: doc "inspect shadow matches for would-be denials". Code: in `Audit` or `DryRun` mode the shadow entry is added when the check fails and when it passes (internal/rebac/engine.go:153-157 and :167-169). Run: `GET /admin/subjects/explain` returns the same `shadow` entry in both cases.
- :63: doc "Templates accept exactly five placeholders". Code: plain text replace (internal/rebac/engine.go:236-245). `{{ table }}` with spaces or an unknown placeholder is sent to OpenFGA as written. No validation error. Run.

opa.md

- :29-30: doc "With engine: opa and no modules loaded, every query fails". Code: `sluice serve` exits 1 at startup: "opaengine: no .rego modules found under ..." or "moduleDir is required" (internal/opaengine/engine.go:73-82). Run.
- :28-29: doc "a broken module set fails the reload". Code: the old modules stay active (true), but the reload reports success. Run: `POST /admin/reload` returned 200 `{"ok":true}`, the log said "reload applied" plus a WARN "policy engine rejected snapshot" (cmd/sluice/bootstrap.go:453). The check before publish compiles YAML only (cmd/sluice/bootstrap.go:402).
- :27-28: doc "Modules are recompiled on every policy reload". True, but a change to a `.rego` file alone starts no reload. The watcher reacts to `.yaml` and `.yml` only (internal/config/watch.go:225-235). Run: needed `POST /admin/reload`.
- :54: `request` values in the input document. See "Not implemented".

classification.md

- :7-8: doc "every mask, filter, deny, or approval". Also true for reject and rewrite policies. Not true for `RelationshipPolicy`: the rebac member compiles selectors without the tag index (internal/rebac/engine.go:92). Run: `sluice policy validate` passes, `sluice serve` with the rebac member exits 1 ("unknown tag").

### Broken code blocks

None. All 25 blocks parse, validate or run. Blocks that are valid but teach something the code does not do:

- subjects.md:104 (corp-idp): `tenantId: $.tenant` has no effect. See "Not implemented".
- guardrails.md:41 and :134: the rules can be bypassed with a subquery or a union. See "Docs and code disagree".
- rebac.md:118: `Audit` mode cannot show would-be denials.
- opa.md:36: the `request` values are never filled at run time.
- rebac.md:40: `cacheTtl` is not in the block (good). `type: openfga` is not checked.

### Proposed pages for this section

Baseline 3319 prose words on six pages. Proposal: about 1970 words on five pages.

1. `policies/guardrails.md`: QueryRejectPolicy and QueryRewritePolicy. One example each with real output, field tables, the `query.*` table with its scope, folding rule. About 350 words.
2. `policies/approvals.md`: ApprovalPolicy. Example, the sequence diagram (unchanged), `when` table, client codes, server config table, webhook payload. Drop the broker design text. About 420 words.
3. `policies/subjects.md`: SubjectBinding. JWT, API key, claims (state what JWT mode really reads), rate limit, budget. About 400 words. Server keys for `budget.*` and `approval.*` belong to reference/configuration.md (generated). Keep a short fragment only.
4. `policies/classification.md`: DataClassification. One worked pair, tag rules, limits. Drop "Governance pattern". About 250 words.
5. `policies/engines.md` (new, merge of rebac.md and opa.md): engine selection (`policies.engine`, `composite.members`), merge rules, then one part for OPA and one for ReBAC. About 550 words. Reason: both pages need the same `policies.*` config and the same composite rules (deny wins, abstain, at least one allow). Today no page owns these rules. They are split between rebac.md:33-36 and opa.md:9-11. After the rewrite each page alone is short. If the two pages stay separate, opa.md must own the composite part and rebac.md must link to it.

No page of this part should be removed without a merge.

### Features the code has that the section does not document

- A JWT is sent as `Authorization: Bearer <token>` (internal/identity/jwt.go:317-327). subjects.md never says so.
- Without `identity.apiKeyPepper` no API key is accepted at all (cmd/sluice/bootstrap.go:645).
- The issuer of an API-key subject is `spec.issuer`, or the binding name (cmd/sluice/bootstrap.go:743-746). Run.
- `rateLimit.burst` missing or 0 means 1 (cmd/sluice/bootstrap.go:685-687).
- `limits.defaultSubjectRps`, `limits.globalRps`, `limits.perIpRps` (internal/config/server.go:215-221).
- `GET /admin/approvals` lists pending requests with reasons, no tokens (internal/transport/admin/routes.go:23). Run.
- The approval token is also accepted in the `X-Approval-Token` header (internal/transport/rest/approvals.go:93-98).
- A full approval queue returns 429 `ERR_RATE_LIMITED` "approval queue full" (internal/queryservice/approval.go:47-54).
- `approval.syncWait` is cut to `rest.requestTimeout` minus 2 s (cmd/sluice/approval.go:178-188).
- `limit.max` also lowers the row cap, so a result that reaches the cap has `truncated: true` and the warning `ERR_RESULT_TRUNCATED` (internal/queryservice/execute.go:312-314). Run.
- With `engine: opa` no YAML policy kind is evaluated on the query path (cmd/sluice/policyengine.go:27-36). Read.
- OPA `input.action` is the statement kind: `SELECT`, `EXPLAIN`, `SET`, `SHOW` (internal/opaengine/contract.go:37-40). Run.

### Enforcement trace per kind

Every kind of this part is enforced at request time. Where each trail runs:

| Kind | Trail | Checked |
|---|---|---|
| QueryRejectPolicy | pkg/apitypes/decoder.go:44, internal/policy/snapshot.go:334-355, engine.go:242-268, conflict.go:70-95, internal/queryservice/execute.go:224-231 | run |
| QueryRewritePolicy | decoder.go:45, snapshot.go:357-366 and :445-481, conflict.go:340-360, internal/rewriter/apply_rewrite.go:18-60, rewriter.go:273-281, execute.go:308-314 | run |
| ApprovalPolicy | decoder.go:46, snapshot.go:368-377, internal/policy/approval.go:15-40, execute.go:251-279, internal/queryservice/approval.go:26-114, internal/approval/broker.go | run |
| SubjectBinding | decoder.go:48, cmd/sluice/bootstrap.go:618-668 (identity), :674-696 (rate limit), cmd/sluice/budgetwire.go:53-78 (budget) | run |
| DataClassification | decoder.go:52, snapshot.go:167-194, internal/policy/selector.go:209-215 and :562-573. Compile time, effect through the selectors | run |
| RelationshipPolicy | decoder.go:51, internal/config/policies.go:133, internal/rebac/engine.go:84-122 and :127-184. Only with the `rebac` composite member | run, fake backend |
| OPA modules (no kind) | `policies.opa.moduleDir`, internal/opaengine/engine.go:72-116, contract.go:114-184 | run |

Fields that are parsed and change nothing. The trail ends at the type:

- `RelationshipPolicy.spec.backend.cacheTtl` (pkg/apitypes/policy_relationship.go:32, no reader).
- `RelationshipPolicy.spec.backend.type` (pkg/apitypes/policy_relationship.go:26, no reader).
- `SubjectBinding.spec.groupsSource` (pkg/apitypes/subjectbinding.go:22, no reader).
- `SubjectBinding.spec.claims.<extra key>` (pkg/apitypes/subjectbinding.go:36, never decoded).
- `SubjectBinding.spec.claims.tenantId` and `allowedRegions` for a JWT binding (only reader is the API-key path, cmd/sluice/bootstrap.go:795-800).
- `QueryRewritePolicy.spec.rewrite.hint` (refused at internal/policy/snapshot.go:446-448, as the docs say).

Notes for the maintainer (code, not docs):

- Standalone `sluice mcp` builds the approval broker and sends the webhook, but serves no REST endpoint (cmd/sluice/mcp.go:73-128). Run: the `accept_url` had no listener. The MCP approval flow needs `sluice serve` with `mcp.enabled`.
- `sluice budget show` opens the store with default settings, so it deletes rows older than 35 days whatever `budget.retentionDays` says (cmd/sluice/budget.go:37, internal/budget/manager.go:77-79, :108). The server deletes old rows only at startup.
- A reload that adds an ApprovalPolicy to a server with no broker makes matching queries fail with `ERR_INTERNAL` (internal/queryservice/execute.go:252-260). The log line says they "pend and expire" (cmd/sluice/bootstrap.go:495).

## docs/policies/guardrails.md

Prose words now: 595. Suggested after rewrite: 350.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-6 | QueryRejectPolicy vetoes statements, QueryRewritePolicy bounds them | implemented (internal/policy/snapshot.go:334, :357) | Enforced at internal/policy/conflict.go:66-95, internal/queryservice/execute.go:224-231 and :308-314, internal/rewriter/apply_rewrite.go:18. Run. |
| 2 | 6-8 | Both act after access control. Order: deny, reject, approval | implemented (internal/policy/conflict.go:51-57, :93) | Approval runs after resolve (internal/policy/engine.go:146). Also stated in approvals.md:7. One page should own it (policies/index.md). |
| 3 | 12 | `spec.reject.rules[]`, list of rules | implemented (pkg/apitypes/policy_queryreject.go:24-36) | At least one rule is required (pkg/apitypes/decoder.go:415). Not in docs. Run. |
| 4 | 16 | `name` is optional, an unnamed rule loads | implemented (pkg/apitypes/decoder.go:411-422) | Run: validate OK. |
| 5 | 16 | `name` identifies the rule in the error and the audit record | mismatch | Not in the error body, not in the audit record. See summary. |
| 6 | 17 | `expression` is CEL over `query.*` and `subject.*` | implemented (internal/policy/cel.go:30-38) | Compiled at snapshot.go:341-350, run at engine.go:242-268. Test `TestCELRejectExpression`. Run. |
| 7 | 17 | `expression` can use `request.*` | not implemented | Always empty at run time. See summary. |
| 8 | 17 | Empty expression fires when the selector matches | implemented (internal/policy/engine.go:249-259) | Test `TestEngine_QueryRejectStatic`. Run. |
| 9 | 18 | `message` is shown to the client | implemented (internal/queryservice/execute.go:495) | Run. |
| 10 | 19 | `code`, default `ACL_REJECTED` | implemented (internal/policy/conflict.go:79-82) | Any string is accepted, no check. HTTP status stays 403. Run: `MY_CODE` gave 403. |
| 11 | 21-31 | `query.*` variables and types | implemented (internal/policy/cel.go:154-175) | All 14 names exist. `query.limit` is 0 when the LIMIT is not a constant. `tables` are lower case. Run for 12 of them. |
| 12 | 21 | The variables describe the parsed statement | mismatch | Top-level SELECT and left arm of a set operation only. See summary. |
| 13 | 33 | No `now` variable | implemented (internal/policy/cel.go:30-38) | Run: compile error "undeclared reference to 'now'". The cache reason is an internal detail. |
| 14 | 33-34 | A rule that errors at run time denies the request | implemented (internal/policy/engine.go:127-140) | Run: outcome deny, `ACL_DENIED`. |
| 15 | 36-39 | Expression must be bool, bare `query.x` does not compile | implemented (internal/policy/cel.go:99-101) | Run: "expression must return bool, got dyn". |
| 16 | 60-62 | A rule with no expression is a veto by selector (group, table pattern) | implemented (internal/policy/engine.go:249-259) | Run for a group. |
| 17 | 61 | Veto by "an IP range" | not implemented | `ipRanges` never matches over REST. See summary. Owner: matching.md. |
| 18 | 66 | `spec.rewrite` needs one of `limit`, `sample`, `timeout` | implemented (internal/policy/snapshot.go:477-479) | Run. |
| 19 | 68-71 | `hint` parses, is refused at compile, fails `policy validate` | implemented (internal/policy/snapshot.go:446-448) | Run: "hints are not supported". The reason text is an internal detail. |
| 20 | 75 | `limit.max` integer 1 to 2147483647 | implemented (internal/policy/snapshot.go:450-455) | Run: 0 and 2147483648 refused. |
| 21 | 75-77 | No LIMIT: injected. Larger constant: clamped. Smaller or equal: kept. Expression: replaced | implemented (internal/rewriter/apply_rewrite.go:33-48) | Run with `policy test`, exact SQL. Golden tests 11 and 12 in internal/rewriter/testdata/rewrites. |
| 22 | 77 | A parameter limit is replaced by the cap | mismatch | `$n` is refused. See summary. |
| 23 | 77-78 | `EXPLAIN`, `SET`, `SHOW` are skipped | implemented (internal/rewriter/apply_rewrite.go:23-30) | Test at internal/rewriter/apply_rewrite_test.go:73. Run. |
| 24 | 78 | `PRAGMA` is skipped | not implemented | Not parseable. See summary. |
| 25 | 78-79 | EXPLAIN is served with no filter or mask, the limit is not applied | implemented (internal/rewriter/apply_rewrite.go:23-30) | Run. |
| 26 | 79-81 | EXPLAIN on a filtered or masked table: `ERR_UNSUPPORTED_SYNTAX` | implemented (internal/rewriter/rewriter.go:180-183) | Run. Belongs to row-filters.md and column-masks.md (duplicate here). The reason is an internal detail. |
| 27 | 92 | `sample.rate` float in (0, 1] | implemented (internal/policy/snapshot.go:457-459) | Run: 0 and 1.5 refused. |
| 28 | 92-93 | `sample.method`: `reservoir` (default), `bernoulli`, `system`, lower case | implemented (internal/policy/snapshot.go:460-468) | Run: `Bernoulli` refused. Test `TestCompileRewrite_SampleDefaultsToReservoir`. |
| 29 | 93 | The SELECT is wrapped in `USING SAMPLE` | implemented (internal/rewriter/apply_rewrite.go:55-60) | Run, exact SQL. |
| 30 | 99 | Sampling applies to SELECT only | implemented (internal/rewriter/rewriter.go:273-281) | Test at apply_rewrite_test.go:99. Run. |
| 31 | 99-100 | Every token is compile-validated | internal detail | |
| 32 | 104 | `timeout` is a duration | implemented (pkg/apitypes/duration.go:28-44) | Go duration string or a bare integer in seconds. |
| 33 | 104-105 | Enforced by canceling execution, never in the SQL | implemented (internal/queryservice/execute.go:309-311) | Executor deadline at internal/executor/executor.go:111. Run: SQL unchanged. Golden test 15. |
| 34 | 105-107 | Can only shorten. `timeout_ms`, `limits.queryTimeout`, `limits.maxQueryTimeout` still apply | implemented (internal/queryservice/execute.go:94-99, :309) | Keys at internal/config/server.go:211-212. Read. |
| 35 | 107 | On expiry `ERR_TIMEOUT` | implemented (internal/queryservice/execute.go:582-583) | HTTP 504. Run. A 1 ms timeout often gives 500. See summary. |
| 36 | 111-113 | Several policies: minimum limit, minimum timeout, first sample in policy order | implemented (internal/policy/conflict.go:340-360) | Order at snapshot.go:157-162. Test `TestCollectRewrites_RestrictiveFold`. Run. |
| 37 | 113-114 | "layer a tenant-wide ceiling ... without coordination" | fluff | |
| 38 | 118-119 | Recipe: agents get 1000 rows and 10 s | implemented | Same paths as rows 21 and 33. |
| 39 | 132 | Recipe: veto `SELECT *` for agents | implemented | Limited by row 12: a star in a subquery passes. Run. |
| 40 | 147 | Recipe: 10% sample on the lake catalog | implemented | Same path as row 29. |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 41 | yaml | QueryRejectPolicy with three rules | untested, verified by hand | Strict validate OK. Three suite cases pass. Bypass cases: see summary. |
| 83 | sql | Limit injection, before and after | untested, verified by hand | `policy test` exact match. Golden test 11 covers the same rewrite on another table. |
| 95 | sql | Sample wrap output | untested, verified by hand | `policy test` exact match. Golden test 13 covers the same form. |
| 121 | yaml | QueryRewritePolicy limit and timeout | untested, verified by hand | Strict validate OK. |
| 134 | yaml | QueryRejectPolicy no-select-star | untested, verified by hand | Strict validate OK. |
| 149 | yaml | QueryRewritePolicy sample | untested, verified by hand | Strict validate OK. |

## docs/policies/approvals.md

Prose words now: 705. Suggested after rewrite: 420.

Mermaid diagram (line 13): all eight labels match the code and the live run. Nothing to report.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5 | ApprovalPolicy parks an allowed query until a human decides | implemented (internal/queryservice/execute.go:251-279) | Decision at internal/policy/approval.go:15-40, wait at internal/queryservice/approval.go:26-114. Run. |
| 2 | 5-7 | Use cases (PII, salary, exports) | fluff | |
| 3 | 7-9 | Order deny, reject, approval | duplicate | Owner: policies/index.md. True (internal/policy/engine.go:146). |
| 4 | 30-31 | Decision inside `syncWait` (default 20 s): the request resumes, no 202 | implemented (internal/queryservice/approval.go:68-90) | Default at internal/config/server.go:356. Run. |
| 5 | 31-32 | Accept and reject URLs need only the token, no login | implemented (internal/transport/rest/routes.go:42-46) | Run. |
| 6 | 32-33 | Unknown id and bad token give the same 404 | implemented (internal/transport/rest/approvals.go:41-44) | Test `TestApproval_OracleFreeness`. Run. |
| 7 | 33 | Prefetch requests never change state | implemented (internal/transport/rest/approvals.go:23-26, :102-112) | HEAD, or header `Purpose`, `Sec-Purpose`, `X-Moz` with `prefetch` or `preview`: 204. Run. |
| 8 | 37 | Empty `when`: the selector match triggers | implemented (internal/policy/approval.go:48-50) | Test `TestApproval_EmptyWhenTriggersOnSelector`. |
| 9 | 37-38 | `columnsAccessed` and `predicates` are OR'd | implemented (internal/policy/approval.go:55-70) | Run. |
| 10 | 40-41 | `columnsAccessed`: wildcards against every referenced column, dotted and bare | implemented (internal/policy/approval.go:75-83) | Columns come from the whole statement, subqueries too (internal/parser/shape.go:56-62). Run. |
| 11 | 42-45 | `predicates[]`: `column` required, `op` list, `*` or empty for any, `value` rules | implemented (pkg/apitypes/decoder.go:332-357) | Match at internal/policy/approval.go:85-96. Test `TestApproval_PredicateTrigger`. Run for `=`. |
| 12 | 47 | `reason` goes into the webhook payload | implemented (internal/policy/approval.go:24-37) | Sent as `policy <name>: <reason>`. Empty reason: "matched approval policy <name>". Run. |
| 13 | 47-48 | `reason` is in the status of `GET /v1/approvals/{id}` and `check_approval` | mismatch | Not returned. See summary. |
| 14 | 48-49 | The pending error has only `approval_id` and `expires_at` | implemented (internal/queryservice/approval.go:107-112) | Run. |
| 15 | 51-54 | Unparseable query with non-empty `when` is parked | mismatch | Returns `ERR_SYNTAX`, nothing is parked. See summary. |
| 16 | 58-59 | Five states | implemented (internal/approval/request.go:12-16) | Run: saw all five. |
| 17 | 61-62 | Same subject and SQL while pending: same id, no second webhook | implemented (internal/approval/broker.go:147-152) | Test `TestBroker_DedupeNoDoubleWebhook`. Run. |
| 18 | 63-64 | `requestTtl` 15 m, `grantTtl` 5 m | implemented (internal/config/server.go:357-358) | Used at broker.go:172 and :296. Run with 12 s and 4 s. |
| 19 | 65-66 | Grant is used once. The next run parks again | implemented (internal/approval/broker.go:330-357) | Test `TestBroker_ConcurrentConsumeGrantExactlyOnce`. Run. |
| 20 | 67-68 | Same verb twice is a no-op, other verb is 409 | implemented (internal/approval/broker.go:315-322) | HTTP at rest/approvals.go:45-49. Run. Also 409 after expiry. A reject after the grant was used is 200. |
| 21 | 69 | `maxPending` 1000, new requests refused | implemented (internal/approval/broker.go:154-157) | Test `TestBroker_MaxPending`. Client code is 429 `ERR_RATE_LIMITED` (not in docs). Read. |
| 22 | 71-72 | In memory by default, a restart drops state | implemented (cmd/sluice/approval.go:55-68) | Read. |
| 23 | 72-75 | `approval.persist: true`: SQLite under `approval.stateDir`, tokens stored as SHA-256 | implemented (cmd/sluice/approval.go:56-67) | Schema at internal/approval/sqlite_store.go:24-45. File is `approvals.db`. Run: hash matched, link worked after restart. |
| 24 | 75-77 | After restart: grants single-use, no webhook again, old requests expire | implemented (internal/approval/broker.go:102-122) | Tests `TestBroker_GrantSurvivesRestartExactlyOnce`, `TestBroker_ExpiredPendingNotResurrected`, `TestApproval_PersistsAcrossBrokerRestart`. Run for the first two points. |
| 25 | 77-78 | A waiting request does not survive a restart | implemented (internal/approval/broker.go:65-70) | Read. |
| 26 | 80-82 | Not replicated, run one instance | implemented (internal/approval/broker.go:58-62) | State is in the process and in one local file. Read. |
| 27 | 82-83 | Audit events `approval-requested`, `-approved`, `-rejected`, `-expired` | implemented (internal/audit/record.go:31-34) | Written at cmd/sluice/approval.go:132-153. Run. No event for `consumed`. |
| 28 | 87-102 | Server keys and defaults under `approval:` | implemented (internal/config/server.go:241-265, :355-363) | All nine keys and three webhook keys exist with these defaults. `config validate` OK. |
| 29 | 90 | `publicBaseUrl` is required when an ApprovalPolicy loads | implemented (cmd/sluice/approval.go:39-41) | Run: `serve` exits 1. |
| 30 | 95 | `sqlSampleBytes: 0` sends no SQL | implemented (internal/queryservice/approval.go:167-175) | Read. |
| 31 | 100 | `headersRef` is a JSON header map | implemented (cmd/sluice/approval.go:92-102) | Run: both headers arrived. |
| 32 | 104 | Three tries with backoff per target | implemented (internal/approval/webhook.go:94-106) | Test `TestWebhook_RetryThenSucceed`. Waits 1 s, then 2 s. |
| 33 | 104-105 | A failed delivery does not fail the query, it is logged | implemented (internal/approval/webhook.go:107-111) | Read. |
| 34 | 109-121 | Webhook payload fields | implemented (internal/approval/webhook.go:51-61) | Run: same nine fields. Test `TestWebhook_PayloadAndCapabilityURLs`. |
| 35 | 127 | Parked, no decision: `ERR_APPROVAL_PENDING`, 202 | implemented (internal/queryservice/approval.go:103-112) | Run. |
| 36 | 128 | Rejected: `ERR_APPROVAL_REJECTED`, 403 | implemented (internal/queryservice/approval.go:91-96) | Only for a request inside `syncWait`. See summary. Run. |
| 37 | 129 | Request timed out: `ERR_APPROVAL_EXPIRED`, 410 | implemented (internal/queryservice/approval.go:97-102) | Only for a request inside `syncWait`. Run. |
| 38 | 129 | Grant timed out: `ERR_APPROVAL_EXPIRED`, 410 | mismatch | The re-run parks again with 202. See summary. |
| 39 | 131 | The 202 body has `approval_id` and `expires_at` in `details` | implemented (internal/queryservice/approval.go:107-112) | Run. |
| 40 | 132-133 | `GET /v1/approvals/{id}`: only the requesting subject, else 404 | implemented (internal/transport/rest/approvals.go:69-82) | Test `TestApproval_StatusOwnerOnly`. Run. No credential: 401. |
| 41 | 133 | Re-run the identical SQL once approved | implemented (internal/queryservice/execute.go:261-268) | Key is the SHA-256 of the raw SQL text. Run. |
| 42 | 134-136 | MCP `check_approval{approval_id}` and `await_approval{approval_id, timeout_seconds}`, capped | implemented (internal/transport/mcp/tools.go:72-121) | Cap is 55 s (:113). Run over stdio. No automated test names these tools. |
| 43 | 141-142 | Recipe fires on a PII column or on `country = 'de'` | implemented | `policy explain` run, four cases. |
| 44 | 159-160 | `Audit` mode records a shadow, queries keep flowing | implemented (internal/policy/approval.go:28-32) | Test `TestApproval_ShadowMode`. Run. |
| 45 | 160 | The shadow is visible in `sluice policy explain` | mismatch | Only with `--json`. See summary. |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 13 | mermaid | Approval sequence | untested, verified by hand | Every step seen in the live run. |
| 87 | yaml | `approval:` server config | untested, verified by hand | `config validate` OK. Ran a server with the same keys. |
| 109 | json | Webhook payload | untested, verified by hand | Live payload has the same field set. Go test checks the fields, not this text. |
| 144 | yaml | ApprovalPolicy on PII columns | untested, verified by hand | Strict validate OK. Near copy in examples/approval-workflow/policies.d/approval.yaml (other selector), no test there. |
| 162 | yaml | ApprovalPolicy in Audit mode | untested, verified by hand | Strict validate OK. `policy explain --json` shows the shadow. |

## docs/policies/subjects.md

Prose words now: 621. Suggested after rewrite: 400.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5 | SubjectBinding sets how callers authenticate and their limits | implemented (cmd/sluice/bootstrap.go:618-668) | Type at pkg/apitypes/subjectbinding.go:7-26. |
| 2 | 5-7 | A subject has id, issuer, email, groups, claims | implemented (internal/identity/userctx.go:27-62) | |
| 3 | 8-9 | No matching binding: 401 `ERR_UNAUTHORIZED` | implemented (internal/identity/middleware.go:61-73) | Run: unknown issuer, unknown key id, no credential. |
| 4 | 13 | One binding per issuer, a duplicate is a load error | implemented (internal/identity/binding.go:94-96) | `serve` only. `policy validate` misses it. See summary. |
| 5 | 13-14 | HS256/384, RS256/384, ES256/384. `alg: none` refused | implemented (internal/identity/jwt.go:39, :166-169) | Run: HS256 and HS384 ok, HS512 and none 401. RS and ES read. Test `TestJWTAlgNoneRejected`. |
| 6 | 14-15 | RS and ES keys from JWKS, HS from `hmacSecretRef` | implemented (internal/identity/jwt.go:245-263) | Test `TestJWTHMACPath`. HS run, JWKS read. |
| 7 | 15-16 | Validates `iss`, `aud`, `exp`, `nbf` | implemented (internal/identity/jwt.go:172-179, :195-228) | Run for all four. `aud` only when `audience` is set. `exp` is required. |
| 8 | 16 | Validates `iat` | mismatch | Not checked. See summary. |
| 9 | 20 | `issuer` | implemented (internal/identity/binding.go:90-97) | |
| 10 | 21 | `audience`, one `aud` entry must match | implemented (internal/identity/jwt.go:224-228, :331-345) | Test `TestJWTAudienceArrayClaim`. Run. |
| 11 | 22 | `jwksUrl` needs `issuer` and `audience` | implemented (pkg/apitypes/decoder.go:496-501) | |
| 12 | 23 | `hmacSecretRef` | implemented (cmd/sluice/bootstrap.go:703) | Run. |
| 13 | 24 | `jwksCacheTtl` | implemented (internal/identity/jwt.go:257-258) | Default 10 m (internal/identity/jwks.go:69-70). Not in docs. |
| 14 | 25 | `clockSkew`, default 60 s | implemented (internal/identity/jwt.go:188-194) | Run: `exp` 30 s in the past accepted. |
| 15 | 30 | `claims.subjectId`, `email`, `groups` map JWT claims | implemented (internal/identity/jwt.go:265-291) | `subjectId` default is `sub`, `email` default is the `email` claim. Run. |
| 16 | 30-31 | `claims.tenantId`, `allowedRegions` map JWT claims | not implemented | See summary. |
| 17 | 31 | Any extra key becomes a named claim | not implemented | See summary. Both modes. |
| 18 | 32-34 | Path grammar: optional `$`, dots, `[n]`. No wildcards, filters, recursion | implemented (internal/identity/claims.go:16-40, :104-141) | Tests `TestExtractClaim`, `TestExtractClaimArrayIndex`, `TestParseClaimPathErrors`. Run for a nested path. |
| 19 | 34 | `groups`: JSON list or comma-separated string | implemented (internal/identity/claims.go:67-101) | Test `TestExtractStringList`. Run. |
| 20 | 36-38 | Extracted values flow into selectors, templates and CEL | mismatch | Groups do. Templates and `subject.claims` read the raw token claims (internal/policy/template.go:113-118, internal/policy/cel.go:118-137), not mapped names. |
| 21 | 42 | `X-Api-Key: <id>.<material>` or `Authorization: ApiKey <id>.<material>` | implemented (internal/identity/apikey.go:187-200) | Split at the last dot (:208-214). Run. Test `TestAPIKeyAcceptsAuthorizationHeaderForm`. |
| 22 | 43-44 | Constant-time compare of HMAC-SHA256(pepper, `id:material`) with the hex hash behind `hashRef` | implemented (internal/identity/apikey.go:153-158, :219-225) | Run: CLI output equals a python HMAC. |
| 23 | 44-46 | No key material stored. Pepper is `identity.apiKeyPepper` | implemented (internal/config/server.go:196-198) | The reason text is an internal detail. |
| 24 | 46-47 | Random timing jitter on failure | internal detail | internal/identity/apikey.go:251-259. |
| 25 | 49-55 | `sluice apikey hash --pepper --id --material` prints the hex hash | implemented (cmd/sluice/apikey.go:35-69) | Test `TestAPIKeyHashCommandMatchesLibrary`. Run. `--pepper` takes a raw value too. |
| 26 | 57 | Store the hex where `hashRef` points | implemented (cmd/sluice/bootstrap.go:754-769) | A bad `hashRef` only logs a warning and skips the key. Test `TestBuildAPIKeyBindings_SkipsBadRef`. |
| 27 | 57-60 | API-key mode: `subjectId` literal (or the key id), `tenantId` and `email` literal | implemented (cmd/sluice/bootstrap.go:774-781, :793-800) | Run. `allowedRegions` is stamped too. |
| 28 | 59 | API-key mode: extra keys are stamped | not implemented | See summary. Run. |
| 29 | 60-61 | Groups come from the key's `groups` | implemented (cmd/sluice/bootstrap.go:770-773) | Empty list: literal `claims.groups` becomes one group. |
| 30 | 61 | `apiKeys[]` has `id`, `hashRef`, `groups` | implemented (pkg/apitypes/subjectbinding.go:53-57) | |
| 31 | 65 | `rateLimit: {rps, burst}`, token bucket per subject | implemented (internal/ratelimit/limiter.go:29-65) | Checked at internal/queryservice/execute.go:57-69. Run. |
| 32 | 65-67 | Resolved by subject id, then issuer | implemented (internal/ratelimit/limiter.go:105-116) | Test `TestLimiter_PerIssuerAppliesPerSubject`. |
| 33 | 66 | An API-key binding limits its subject | mismatch | Only with `claims.subjectId` or `spec.issuer`. See summary. |
| 34 | 67-68 | `rps: 0` or no block: unlimited | mismatch | `limits.defaultSubjectRps` applies. See summary. |
| 35 | 68-69 | Anonymous requests are bounded by `limits.maxConcurrent` | mismatch | REST has no anonymous requests. See summary. |
| 36 | 69 | Over limit: `ERR_RATE_LIMITED`, 429 | implemented (internal/queryservice/execute.go:58-68) | Run. |
| 37 | 70 | Hot-reloadable, a changed spec resets the bucket | mismatch | Reload works (cmd/sluice/bootstrap.go:481-484). Every reload resets all buckets. |
| 38 | 74 | `budget: {cpuSecondsPerDay, rowsPerDay}` per UTC day, 0 is unlimited | implemented (internal/budget/manager.go:125-144) | Needs `budget.enabled`. Same key gap as row 33. Run. |
| 39 | 75 | Usage is CPU milliseconds and rows | mismatch | Wall-clock time, not CPU. See summary. |
| 40 | 75-77 | Recorded when the query completes, kept in memory, flushed to SQLite | implemented (internal/queryservice/audit_helpers.go:320-321) | Flush at internal/budget/manager.go:259-293. Test `TestBudget_SurvivesRestart`. Run. |
| 41 | 77 | Counters reset at UTC midnight | implemented (internal/budget/manager.go:217-224) | Read. |
| 42 | 77-78 | Exhausted: `ERR_BUDGET_EXCEEDED`, 429 | implemented (internal/budget/manager.go:137-142) | Run: "daily row budget exhausted". |
| 43 | 78-80 | Checked before, charged after. One big query can overshoot | implemented (internal/queryservice/execute.go:73-83, :418-423) | Run. "treat budgets as a daily backstop" is fluff. |
| 44 | 86-93 | Server keys under `identity:` and `budget:` with defaults | implemented (internal/config/server.go:230-236, :364-370) | `config validate` OK. Store file is `<stateDir>/budget.db`. |
| 45 | 92 | `failClosed`: store unavailable refuses queries | mismatch | Startup only. See summary. |
| 46 | 93 | `retentionDays: 35` | implemented (internal/budget/manager.go:296-301) | Old rows are deleted only at process start (:108). |
| 47 | 96-97 | `sluice budget show <subject> [--state-dir] [--day] [--json]` | implemented (cmd/sluice/budget.go:25-60) | Run: `subject research-agent: cpu_seconds=0 rows=5`. Reads the file, so it lags by `flushInterval`. |
| 48 | 101-102 | Recipe: JWKS, claims for selectors and tenant templates | not implemented | The `tenantId: $.tenant` line has no effect. `groups` works. |
| 49 | 102 | Recipe: "generous shared rate limit" | mismatch | One bucket per subject. See summary. |
| 50 | 122 | Recipe: API key with fixed identity, rate limit and budget | implemented | Run with the same shape of binding. |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 51 | console | `sluice apikey hash` | tested | cmd/sluice/apikey_test.go `TestAPIKeyHashCommandMatchesLibrary` runs the command with a raw pepper. The `secret://env/...` pepper form was run by hand: 64 hex characters, equal to a python HMAC. |
| 84 | yaml | `identity:` and `budget:` server config | untested, verified by hand | `config validate` OK. Ran a server with these keys. Comment on `failClosed` is wrong. |
| 104 | yaml | SubjectBinding corp-idp (JWKS) | untested, verified by hand | Strict validate OK. Ran the same shape with HS256. `tenantId: $.tenant` does nothing. JWKS fetch not run. |
| 124 | yaml | SubjectBinding research-agent | untested, verified by hand | Strict validate OK. Rate limit and budget enforced in the live run. |

## docs/policies/classification.md

Prose words now: 479. Suggested after rewrite: 250.

What a tag does at run time: nothing by itself. Tags are resolved when policies compile. A selector with `resources.tags` then matches the tables of the tagged rules. A mask also takes its columns from the rules. There is no inheritance. Tags are declared only in `DataClassification` objects.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5 | `DataClassification`, apiVersion `sluice.bino.bi/v1beta1` | implemented (pkg/apitypes/decoder.go:52) | Run: `v1alpha1` is "unknown kind". |
| 2 | 5-6 | Tags let policies match on what data is | implemented (internal/policy/snapshot.go:167-194) | Selector side at internal/policy/selector.go:209-215. |
| 3 | 6-9 | Every mask, filter, deny, approval with `tags` follows the classification | implemented (internal/policy/snapshot.go:384-391) | Also reject and rewrite. Not RelationshipPolicy. See summary. Run: mask, deny, reject. |
| 4 | 8-9 | Tables added later are covered | implemented (internal/policy/snapshot.go:142) | Index is rebuilt on each compile. Read. |
| 5 | 25-26 | `spec.rules[]`: selector plus tags, at least one tag | implemented (pkg/apitypes/decoder.go:365-371) | At least one rule too. Run. |
| 6 | 27-28 | Tag syntax | implemented (pkg/apitypes/decoder.go:362) | `^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$`. Run: `PII` and `pii.` refused. |
| 7 | 29-31 | Rule fields `catalogs`, `schemas`, `tables`, `columns`. No `tags`, no `actions` | implemented (pkg/apitypes/decoder.go:377-382) | Run. |
| 8 | 35-37 | Used at compile time, on every load and reload | implemented (internal/policy/snapshot.go:142-146) | "tag to resource index" is an internal detail. |
| 9 | 39-41 | Tags in one list are OR'd, name fields are AND'd on top | implemented (internal/policy/selector.go:562-573) | Test `TestTags_TagOnlySelectorMatchesOnlyTaggedTables`. Run. |
| 10 | 42-44 | Unknown tag: compile error, snapshot refused, old one stays on reload | implemented (internal/policy/selector.go:209-213) | Reload check at cmd/sluice/bootstrap.go:402. Test `TestTags_UnknownTagFailsCompile`. Run: same message. |
| 11 | 44 | "A typo'd tag can never silently protect nothing" | fluff | |
| 12 | 45 | A rule with only `columns` matches every table | implemented (internal/policy/selector.go:547-560) | Run. |
| 13 | 46-48 | `effect: allow` by such a tag is a compile error | implemented (internal/policy/selector.go:270-283) | Test `TestTags_AllowByColumnOnlyTagIsRefused`. Run. |
| 14 | 48-49 | A mask by such a tag needs a schema cache entry for each table | implemented (internal/policy/conflict.go:282-293) | Run: table without schema entry fails in `policy test`. Owner: column-masks.md. |
| 15 | 50 | File order does not matter | implemented (internal/policy/snapshot.go:142-146) | |
| 16 | 52-54 | A mask by tag masks the columns of the matching rules | implemented (internal/policy/conflict.go:372-398) | Test `TestTags_ColumnMaskViaClassification`. Run. |
| 17 | 58-61 | Two teams can own classification and enforcement | fluff | |
| 18 | 61-63 | `sluice policy validate --strict` fails when a used tag is removed | implemented (cmd/sluice/policy.go:72-76) | Run. |
| 19 | 93-94 | Worked pair: `email` and `phone` masked on customers, other tables untouched | implemented | `policy test` run. Test `TestTags_TableOutsideClassificationNotMasked`. |
| 20 | 96 | Recipe: deny tagged tables except for auditors | implemented | `policy test` run, four cases. |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 13 | yaml | DataClassification pii-catalog | untested, verified by hand | Strict validate OK. |
| 70 | yaml | Classification plus ColumnMaskPolicy by tag | untested, verified by hand | Strict validate OK. Suite c1: both cases pass. internal/policy/tags_test.go tests the same idea with other YAML. |
| 98 | yaml | Classification plus deny by tag | untested, verified by hand | Strict validate OK. Suite c2: four cases pass. |

## docs/policies/rebac.md

Prose words now: 425. Suggested after rewrite: 250 (as a part of the merged engines page).

Wiring: reachable from config. `policies.engine: composite` with `rebac` in `composite.members` builds the member (cmd/sluice/policyengine.go:73-79). The run used a python fake of `POST /stores/{id}/check`. A real OpenFGA server was not used.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-6 | `RelationshipPolicy` (v1beta1) asks an OpenFGA store per table | implemented (internal/rebac/engine.go:127-184) | Kind at pkg/apitypes/decoder.go:51. Run. |
| 2 | 6-9 | When to use it | fluff | |
| 3 | 11-14 | Gates a table per query, does not filter rows | implemented (internal/rebac/engine.go:144-172) | |
| 4 | 18-19 | Runs only as the `rebac` composite member. Without it the objects load and do nothing | implemented (cmd/sluice/policyengine.go:73-79) | The YAML engine skips the kind (internal/policy/snapshot.go:380-381). Read for the second part. |
| 5 | 23-30 | Server keys `policies.engine`, `composite.members`, `rebac.cacheTtl` (10 s), `rebac.cacheSize` (10000) | implemented (internal/config/server.go:93-119, :316-322) | `config validate` OK. Run with 3 s. |
| 6 | 33-35 | With `[yaml, rebac]` a query needs a YAML allow and all checks | implemented (internal/policy/composite.go:76-113) | Test `TestReBAC_CompositeNeedsTheYAMLAllow`. Run. |
| 7 | 35-36 | No RelationshipPolicy matches: the member abstains | implemented (internal/rebac/engine.go:174-176) | Test `TestReBAC_AbstainWhenNoPolicyMatches`. Run. |
| 8 | 52 | `authorizationModelId` is optional | implemented (internal/rebac/client.go:55, :72) | Run: sent as `authorization_model_id`. |
| 9 | 53 | `tokenRef` is optional, sent as Bearer | implemented (internal/rebac/client.go:83-85) | Resolved at engine.go:109-116. Run. |
| 10 | 54 | `timeout`, default 3 s | implemented (internal/rebac/client.go:44-47) | Read. |
| 11 | 61-62 | `endpoint`, `storeId` required. One check with `objectTemplate` and `relation` | implemented (pkg/apitypes/decoder.go:391-407) | Run. |
| 12 | 62 | Only backend type `openfga` is supported | mismatch | `type` is never read. See summary. |
| 13 | 62-64 | Five placeholders | implemented (internal/rebac/engine.go:236-245) | Exact text only, no validation. See summary. |
| 14 | 58, 64 | `subjectTemplate` default `user:{{subject.id}}` | implemented (internal/rebac/engine.go:191-194) | Run. |
| 15 | 68-70 | Per matched table every check: `POST <endpoint>/stores/<storeId>/check` with `tuple_key` | implemented (internal/rebac/client.go:68-99) | Run: path and body as documented. Stops at the first failed check. |
| 16 | 74 | All checks true: abstain, another member must allow | implemented (internal/rebac/engine.go:177-183) | Test `TestReBAC_PassingChecksDoNotGrant`. Run. |
| 17 | 75 | One check false: `ACL_DENIED`, "relationship check failed on <table>" | implemented (internal/rebac/engine.go:158-165) | Test `TestReBAC_DenyWhenNotRelated`. Run: 403. |
| 18 | 76 | Backend error, non-2xx, timeout: the query fails | implemented (internal/rebac/engine.go:149-152) | Test `TestReBAC_BackendErrorFailsClosed`. Run: 500 `ERR_INTERNAL`. |
| 19 | 77 | No policy matched: abstain | implemented (internal/rebac/engine.go:174-176) | Same as row 7. |
| 20 | 79-80 | `Audit` and `DryRun` record shadow matches, seen in `GET /admin/subjects/explain` | implemented (internal/rebac/engine.go:142, :153-157) | Run. The endpoint needs `user` and `table`. |
| 21 | 80-81, 114-116 | Shadow matches show would-be denials | mismatch | Same entry for pass and fail. See summary. |
| 22 | 81 | `exclude` removes tables one by one | implemented (internal/rebac/engine.go:145-147) | Read. |
| 23 | 83-84 | Results, positive and negative, are cached | implemented (internal/rebac/engine.go:196-207) | Test `TestReBAC_CachesChecks`. Run. The key format is an internal detail. |
| 24 | 84-86 | TTL comes from `policies.rebac.cacheTtl` only. `spec.backend.cacheTtl` is ignored | implemented (internal/rebac/engine.go:63-77) | Run: policy said 60 s, refetch after 3 s. Drop the ignored field from the docs. |
| 25 | 86 | Errors are never cached | implemented (internal/rebac/engine.go:203-207) | Run. |
| 26 | 86-87 | Cache is purged on every reload | implemented (internal/rebac/engine.go:120) | Read. |
| 27 | 87-88 | One round trip per uncached table and check | implemented (internal/rebac/engine.go:186-213) | Run: call counts matched. |
| 28 | 92-94 | The OpenFGA model needs a `table` type with a `viewer` relation | internal detail | About the OpenFGA side. Not checked here. Keep one line as a hint. |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 21 | yaml | `policies:` server config with rebac member | untested, verified by hand | `config validate` OK. Ran a server with it. |
| 40 | yaml | RelationshipPolicy with all backend fields | untested, verified by hand | Strict validate OK. Ran the same shape against the fake backend. |
| 96 | yaml | RelationshipPolicy viewer on a catalog | untested, verified by hand | Strict validate OK. OpenFGA model text not verified (no OpenFGA server). |
| 118 | yaml | RelationshipPolicy in Audit mode | untested, verified by hand | Strict validate OK. Run: query passes, shadow entry is the same for pass and fail. |

## docs/policies/opa.md

Prose words now: 494. Suggested after rewrite: 300 (as a part of the merged engines page).

Wiring: reachable from config. `policies.engine: opa` uses the OPA engine alone. `composite` with `opa` in the members merges it (cmd/sluice/policyengine.go:27-43, :63-72). Both ran live.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-6 | Embedded OPA engine, alone or next to YAML | implemented (cmd/sluice/policyengine.go:27-43) | Run in both modes. |
| 2 | 6-8 | When to choose Rego | fluff | |
| 3 | 8-9 | `sluice policy test` covers YAML | duplicate | Same as line 121. Owner: testing.md. |
| 4 | 9-11 | `composite` merges both, deny wins | implemented (internal/policy/composite.go:76-147) | Tests `TestComposite_DenyOverrides`, `TestComposite_AbstainedMergesGrants`. Run: Rego filter plus YAML mask and limit. |
| 5 | 19 | `engine`: `yaml` (default), `opa`, `composite` | implemented (cmd/sluice/policyengine.go:23-47) | Default at internal/config/server.go:319. |
| 6 | 23, 27 | `opa.moduleDir`: `*.rego` files at the top level only | implemented (internal/opaengine/engine.go:146-164) | Run: a broken file in a subdirectory was ignored. |
| 7 | 24 | `opa.query`, default `data.sluice.main` | implemented (internal/config/server.go:321) | Run. |
| 8 | 27-28 | Modules are recompiled on every reload | implemented (internal/opaengine/engine.go:72-93) | A `.rego` change alone starts no reload. See summary. |
| 9 | 28-29 | Broken modules fail the reload, old modules stay | mismatch | Old modules stay. The reload reports success. See summary. |
| 10 | 29-30 | `engine: opa` with no modules: every query fails | mismatch | The server does not start. See summary. |
| 11 | 34-56 | Input document: `subject`, `action`, `tables`, `shape` | implemented (internal/opaengine/contract.go:18-71) | Run: dumped the real input, same keys. Table keys are as written in the SQL, lower case in the run. |
| 12 | 54 | Input `request` with remote IP, user agent, headers | not implemented | Always empty. See summary. |
| 13 | 60-61 | Unknown output fields: the query fails | implemented (internal/opaengine/contract.go:115-120) | Test `TestOPA_MalformedOutputFailsClosed`. Run: 500 `ERR_INTERNAL`. |
| 14 | 61-62 | Undefined result: deny | implemented (internal/opaengine/engine.go:107-110) | Run: 403 "opa: no decision (fail-closed)". |
| 15 | 66 | `allow` false or absent denies | implemented (internal/opaengine/contract.go:127-137) | Run: 403 "denied by OPA policy". |
| 16 | 67 | `abstain` | implemented (internal/opaengine/contract.go:122-126) | Test `TestOPA_Abstain`. Run alone: 403 "opa: abstained". |
| 17 | 68 | `deny_reason {message, code, policy}` | implemented (internal/opaengine/contract.go:129-135) | Run. |
| 18 | 69 | `row_filters [{table, combine, predicate}]`, `combine` default `restrictive` | implemented (internal/opaengine/contract.go:147-160) | Test `TestOPA_RowFilterFromRego`. Run. |
| 19 | 70 | `column_masks [{table, column, mask}]` | implemented (internal/opaengine/contract.go:162-172) | Run with `partial`. |
| 20 | 71 | `rejections [{rule, message, code}]` turn an allow into a reject | implemented (internal/opaengine/contract.go:174-183) | Run: 403 `ACL_REJECTED`. |
| 21 | 73-75 | Predicates and masks use the YAML compile paths, templates become bound parameters | implemented (internal/policy/export.go:17-25) | Run with `{{ subject.tenantId }}`. "Rego never renders SQL" is an internal detail. |
| 22 | 75-77 | Invalid predicate or mask, or a table outside the input: the query fails | implemented (internal/opaengine/contract.go:148-150, :163-165) | Test `TestOPA_UnknownTableFailsClosed`. Run: 500 `ERR_INTERNAL`. |
| 23 | 77-79 | Unknown codes fall back to `ACL_DENIED` or `ACL_REJECTED` | implemented (internal/opaengine/contract.go:194-209) | Run: `MY_CODE` replaced, `ERR_TIMEOUT` kept. HTTP stays 403. |
| 24 | 83-84 | The module allows `analysts` on `shop` and adds a tenant filter | implemented | Run alone and in a composite: two rows for tenant acme. |
| 25 | 116-117 | Predicates use the YAML operator names | implemented (internal/opaengine/contract.go:151) | Run: `Equals`, `In`. A wrong name fails the query. |
| 26 | 121-122 | `sluice policy test` uses the YAML engine only | implemented (internal/policytest/policytest.go:106) | |
| 27 | 122-124 | Test Rego with `opa test` and with real queries | internal detail | Advice about an external tool. Sluice ships no `opa` command. Keep one line. |
| 28 | 127-128 | `sluice policy explain` uses the YAML snapshot only | implemented (cmd/sluice/policy.go:144-147) | |
| 29 | 128-130 | `GET /admin/subjects/explain` runs the configured engine, with `OpaModule` entries | implemented (internal/queryservice/tables.go:52) | Entry name is `opa/<query>` (internal/opaengine/engine.go:130). Run. |
| 30 | 130-132 | Header `X-Sluice-Applied-Policies: opa`, audit kind and name `OpaModule` and `opa` | implemented (internal/opaengine/contract.go:144) | Header at internal/transport/rest/query.go:66-67. Run. |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 15 | yaml | `policies:` server config with opa member | untested, verified by hand | `config validate` OK. Ran a server with it. |
| 36 | json | Input document | untested, verified by hand | Valid JSON. Keys match the live dump. The `request` values are never filled at run time. |
| 86 | rego | Complete module | untested, verified by hand | Loaded with `engine: opa` and in a composite. Output as described. internal/opaengine/engine_test.go tests similar modules, not this text. |
