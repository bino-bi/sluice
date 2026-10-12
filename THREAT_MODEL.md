<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# Threat Model

This document is the single source of truth for how Sluice reasons about adversaries. The public docs site links here; `SECURITY.md` covers the disclosure process. Updates follow the governance process (RFC for material changes, PR review for clarifications).

---

## 1. Scope

Sluice is a SQL access-control gateway. Clients send SQL; Sluice parses it, evaluates a declarative policy against the caller's identity, rewrites the statement to enforce the policy, executes it against DuckDB-attached data sources, and emits a hash-chained audit record of the outcome.

**In scope** for this threat model:

- Sluice's own code under `cmd/`, `internal/`, `pkg/`.
- The YAML configuration surface (`ServerConfig` + policy directory).
- The three transports: REST (`/v1/...`), MCP (stdio + Streamable HTTP), admin (`/admin/...`).
- Audit durability and integrity.
- Secret handling (`secret://` resolution, API-key pepper, JWKS keys, driver credentials).
- Interactions with DuckDB and the six first-party data source drivers.

**Out of scope** (owned by the operator or upstream projects):

- The security of the upstream databases themselves. Sluice assumes Postgres, MySQL, and the S3 bucket each enforce their own access controls and rotate their own credentials.
- Physical and network security of the host running Sluice.
- The browser-side security of BI tools talking to Sluice.
- CVEs in transitive dependencies that do not have an exploitable path through Sluice code — we monitor `govulncheck` and patch, but the dependency project owns the fix.

## 2. Assets and trust boundaries

| Asset                          | Why it matters                                           | Owner              |
|--------------------------------|----------------------------------------------------------|--------------------|
| Query results                  | May contain PII, financial data, anything in the DBs.    | Sluice + upstream  |
| Policy snapshot                | Defines who sees what; tamper == bypass.                 | Sluice             |
| Identity material              | JWT signing keys, API-key pepper, HMAC secrets.          | Sluice + KMS       |
| Driver credentials             | Database passwords, S3 keys, MotherDuck tokens.          | Sluice + KMS       |
| Audit log                      | Evidence of every query; downstream compliance depends.  | Sluice + WORM      |
| Audit genesis seed             | Anchors the hash chain; losing it enables silent resets. | Sluice + KMS       |
| Admin token                    | Grants read access to policies + subject explain.        | Sluice             |

Trust boundaries (outermost → innermost):

1. **Public network** (internet or tenant network) ↔ **Sluice REST/MCP listener**.
2. **Sluice process** ↔ **DuckDB embedded engine** (in-process, but hardened).
3. **Sluice process** ↔ **upstream databases** (network, TLS).
4. **Sluice process** ↔ **local filesystem** (config, audit, secrets via `file://`).
5. **Operator console** ↔ **admin listener** (typically private network only).

## 3. Data flow (summary)

```
client ──REST/MCP──▶ identity ──▶ queryservice
                                     │
                                     ├─▶ parser   (pg_query AST)
                                     ├─▶ policy   (atomic snapshot)
                                     ├─▶ rewriter (AST → deparse)
                                     ├─▶ executor (hardened DuckDB)
                                     │       │
                                     │       └─▶ driver.Attach ──▶ upstream DB
                                     │
                                     └─▶ audit    (canonical JSON + hash chain)
```

Every terminal path emits exactly one audit record. The policy decision, the rewritten SQL, the parameter list, and the outcome (success/deny/reject/error/timeout) are all captured. Streaming paths emit on iterator close.

## 4. STRIDE per component

### 4.1 Parser (`internal/parser`, `internal/pgquery`)

- **Spoofing / Tampering:** N/A — parser is internal, takes bytes in and returns an AST.
- **Repudiation:** N/A — no side effects.
- **Information disclosure:** a malformed input must not leak internal state (panic, stack trace). Mitigated by `recover()` in handlers, `ParseError` with line/column only, and `FuzzParse` running nightly.
- **Denial of service:** algorithmic complexity and memory explosion from deeply nested queries. Mitigated by request body cap (`http.MaxBytesReader`, 1 MiB default) and a query-timeout context. `ErrInputTooLarge` sentinel is emitted before parsing is attempted. The body cap and timeout do not stop a nesting-depth attack: pg_query parses and deparses in C, and a statement nested past a few thousand levels (well under 1 MiB) overflows the C stack and kills the process before any timeout fires. A nesting-depth check on the token stream runs before pg_query and refuses such a statement with `ErrTooDeep` (`ERR_UNSUPPORTED_SYNTAX`); flat constructs are not limited. The depth limits are calibrated to the default 8 MiB Linux thread stack (the crash depth scales with the C stack size); a deployment that runs with a smaller thread stack (`ulimit -s`) must lower the constants in `internal/pgquery/depth.go`.
- **Elevation of privilege:** pg_query cuts an identifier at 63 bytes and DuckDB does not. Policy would then match another name than DuckDB reads: a deny or row filter on a longer table name would not apply, and a CTE named with the first 63 bytes would hide the table. The parser refuses any identifier longer than 63 bytes (a quoted name counts after decoding, the content of a `U&"..."` name as written) with `ErrIdentifierTooLong` (`ERR_UNSUPPORTED_SYNTAX`), in `Parse` and in `Fingerprint`. Policy still runs on the regex fallback first, so a deny on the full name wins.

### 4.2 Policy engine (`internal/policy`)

- **Spoofing:** identity is established upstream (identity middleware) — policy trusts `UserCtx` from context.
- **Tampering:** the snapshot is held in `atomic.Pointer`; writers must go through `ApplySnapshot` which is only invoked by the reload path. No in-place mutation.
- **Repudiation:** every evaluation emits an audit record with the full `Decision` (applied policies, active filters, active masks, reject reasons).
- **Information disclosure:** `policy.Engine.Explain` proxies to the same evaluator as `Execute` — admin can inspect decisions without running the query, and vice-versa; no divergent code path.
- **Denial of service:** CEL / template rendering must be bounded. CEL is currently declared-only; the template engine compiles once at load and renders via positional parameters (no dynamic code generation per query).
- **Elevation of privilege:** default-deny when no `SqlAccessPolicy` allows. Verified by an invariant test on every policy-touching slice.

### 4.3 Rewriter (`internal/rewriter`)

- **Spoofing / repudiation:** N/A.
- **Tampering:** the rewriter never concatenates subject/request values into SQL. Templates are rendered to positional `$N` params and passed alongside the rewritten statement. Caller params are not bound, and a `$n` in the caller's statement would read these values (a hash salt, a tenant id). So a statement with a `$n` is refused with `ERR_UNSUPPORTED_SYNTAX` on every path, in the query service before the rewrite cache and in the rewriter. A SQL-injection invariant test asserts no literal user data leaks into the deparsed output when a row filter uses `{{ subject.tenant_id }}`.
- **Tampering (DuckDB dialect):** on the mutation path the rewriter deparses the pg_query tree, and pg_query writes SQL for PostgreSQL. DuckDB runs that text and reads some forms in another way: `INTERVAL '30' DAY` as 30 seconds, `(a OR b) IS NULL` without its parentheses, `**` and `//` with another precedence, a bare `FLOAT` as `DOUBLE`. Right after the clone, before any policy SQL is added, the rewriter rewrites the caller's node shapes it knows into the form DuckDB builds: `LIKE ... ESCAPE`, `INTERVAL 'n' <unit>` with the six units both parsers know, the `pg_catalog` types `json`, `bit` and `varbit`, a bare `FLOAT`, the letter case of alias names, and the spelling of an `EXTRACT` unit that is not a DuckDB keyword (`EPOCH`, `ISODOW`). Then it deparses the clone and asks a separate DuckDB instance (`Options.ParseCheck`, built by `executor.NewParseCheck`: one thread, two connections, nothing attached, hardened like the query executor) for `json_serialize_sql` of the caller's text and of the deparse. Both texts are bound as parameters and never run. The comparator skips query locations, reads the schema `main` as no schema, compares string constants exactly and everything else without regard to ASCII case. Trees that differ give `ERR_UNSUPPORTED_SYNTAX`, a caller's text that DuckDB cannot parse gives `ERR_SYNTAX`, a checker failure gives `ERR_INTERNAL`, and a context that ends during the check gives `ERR_CANCELED` or `ERR_TIMEOUT`. The check is the guarantee: a wrong normalizer makes the trees differ, so it causes a refusal, not wrong rows. A mask snippet from a `pkg/mask` provider is normalized and checked the same way, once per distinct text (the verdicts of up to 4096 texts are kept). It becomes a whole select item of the table subquery, the position it is checked in, so no caller expression can regroup it. A snippet that fails is `ErrMaskUnsupported` (`ERR_INTERNAL`). The check compares DuckDB with DuckDB on the text before any mutation, so it sees neither name resolution nor what the mutators add. Those have targeted refusals: the alias and position of a post-query masked item and the wrapper alias (below), identifiers over 63 bytes (§4.1), and a column qualified with the schema or catalog of a table sluice wrapped under its own name (`main.t.id`, `ERR_UNSUPPORTED_SYNTAX`). `TestDifferential` runs about 840 corpus statements raw and served on two databases: an oracle that holds only the filtered rows and the mask output, and the full data. `TestDifferentialKeywords` puts every `duckdb_keywords()` word, quoted, where pg_query or a mutator writes a name.
- **Information disclosure:** the deparsed SQL **is** logged (to audit), and by design — that is the record of what actually ran. The parameter values are also captured. Operators who must redact values can configure a downstream audit sink that masks before forwarding.
- **Information disclosure (EXPLAIN):** `EXPLAIN` would print the rewritten plan to the client: the policy SQL with its bound values (mask salts, tenant ids) and row estimates or counts. `EXPLAIN` in any form, `ANALYZE` included, is refused with `ERR_UNSUPPORTED_SYNTAX` when the decision has a row filter or column mask.
- **Information disclosure (table wrappers):** every table with a row filter or column mask is read only through a subquery the rewriter builds, `(SELECT ... FROM <table> sluice_src WHERE <filter>) <alias>`. For a masked table the subquery lists exactly the columns of its schema cache entry, in table order, and applies the SQL masks there and nowhere else. So no expression of the query sees a raw SQL-masked value: joins (`USING` and `NATURAL` too), sort and group keys, the whole row, `#N`, stars and subqueries only read the mask output. A post-query masked column passes through raw and is guarded by the rules below. A masked table without a cache entry is refused (`ERR_REWRITE_FAILED`), and a schema load that fails is `ERR_DATASOURCE_UNAVAILABLE`. A column the entry does not list is not read, so a stale entry hides a column added at the source instead of serving it raw. This is not a control: any lookup that misses a table of the catalog loads the catalog again, and a caller can cause one, so add the mask before the column. The filter and the mask inputs are qualified with `sluice_src`, and a statement that contains this name anywhere, in a name or a string, is refused with `ERR_UNSUPPORTED_SYNTAX`, so a FROM item or struct column the caller names after the table cannot take their place. After the wrap, a reflection walk over the final tree refuses any filtered or masked table that is still read outside its subquery. Residual risk: Sluice does not check that a filter column exists in the table. When it does not (a wildcard table pattern, a column dropped at the source), a column named `sluice_src` that the statement builds at run time, for example a FROM subquery that selects `unnest(json_transform(..., '{"sluice_' || 'src": ...}'))`, takes the place of the table in the predicate, and the rows are served unfiltered. Keep the filter column in every table a filter matches.
- **Information disclosure (post-query masks):** `fpe`, `fake`, `jitter` and `hmac_sha256` masks are applied in Go by result column position, so DuckDB computes on the raw value. The rewriter therefore refuses (`ERR_MASK_UNSUPPORTED_CONTEXT`) every form in which DuckDB reads that value or moves the column: the masked column outside a bare item of the outer select list (also in the condition of any join of the FROM tree), the select alias of a masked item anywhere else in the statement except the bodies of its own `WITH` clause (DuckDB resolves a select alias in every clause and in nested SELECTs, but not there), its position in `ORDER BY`, `GROUP BY` (also in `ROLLUP`, `CUBE`, `GROUPING SETS`) or `DISTINCT ON`, `SELECT DISTINCT`, `unnest` in the select list, and a `USING` or `NATURAL` join where a side is a table with a post-query mask. The rewriter does not model where DuckDB binds a name (a join condition does not see the FROM items after it and binds the name to an outer table). So it also refuses, whatever else the name could mean: in a nested SELECT an unqualified name that is a post-query masked column of any table in scope, also when a table of the nested SELECT has a column of that name, and a column reference whose last name is the alias of a table with a post-query mask (the table name when it has no alias), also as a column name (`o.placed_at`). A bare `CURRENT_DATE`, `CURRENT_TIME`, `CURRENT_TIMESTAMP`, `LOCALTIME` or `LOCALTIMESTAMP` is checked as an unqualified column of that name in all these rules: pg_query parses the word as a function, and DuckDB binds it to a column, a select alias or a table of that name first. On a query with a post-query mask these forms are refused on every table, masked or not, because the guards and the star expansion follow a column only by its own name under a FROM name: a column alias list on a table (a CTE reference counts as a table), a join alias, two FROM items under one name, a star inside an expression, `#N`, the `alias.` prefix (DuckDB reads `alias.x` as the select alias `x`, also when a FROM item is called `alias`), a table called `alias` by its name or its alias (star expansion writes `alias.<column>` for it, which reads a select alias when the cache entry lists a column the source no longer has), and every column reference whose qualifier is not a FROM item of its SELECT or of a SELECT around it. The last rule is an allow-list: a qualifier the rewriter does not know (a schema or catalog in front of a table, a column in front of a STRUCT field) is refused, whatever DuckDB would resolve it to. A star is expanded from the schema cache in the outer select list, and in a nested SELECT when its own FROM holds a table with a post-query mask, where the masked column is then refused. DuckDB expands a star over the FROM items of its own SELECT only (checked on DuckDB 1.5.5; the differential corpus holds lines that fail if that changes). For two forms the rewriter does not rely on that: in a nested SELECT it refuses a star that names a table of an outer SELECT with a post-query mask and no FROM item of its own, and a bare star without `FROM` (a star that is the whole select list of `EXISTS` is replaced by a constant before that). A bare star with a `FROM` stays as it is. As a backstop the query service compares the result with the plan (column count, and the column name at each masked position, ASCII case folded; under a sample policy also the `name_N` form DuckDB gives a duplicate name) and refuses a mismatch before any row is served.
- **Denial of service:** a table with a column mask needs its columns from the schema cache, and so does `SELECT *` on a query with a post-query mask. A miss loads the whole catalog again, and there is no negative cache. So a caller whose policy masks a wildcard table pattern (or has a mask rule that names only columns) can make each request introspect the catalog by naming a table that does not exist. The cache holds at most 10,000 tables: a catalog with more can evict its own entries while it loads, and an evicted masked table is refused with `ERR_REWRITE_FAILED` after another full load. Each masked query also indexes the cached entries once to match names without regard to case. Requests that wait for a catalog load share its result. A column dropped at the source makes every query on a masked table fail with `ERR_QUERY_FAILED` until the catalog is loaded again (a reload or a cache miss). With `cache.rewrite.enabled`, a repeated query keeps the old column list for up to `cache.rewrite.ttl` after that load. Deparse is bounded by the AST size, which is bounded by the request body cap. The DuckDB parse check adds one call per mutation-path `SELECT` that misses the rewrite cache. Its cost grows with the statement size, which `limits.maxSqlBytes` bounds, and it runs on its own instance, so it never waits for or holds a query connection. The `json_serialize_sql` trees of a long `IN` list take about 400 bytes of DuckDB memory per byte of text, so the instance gets 1024 bytes per byte of `limits.maxSqlBytes` (at least 256 MiB) for its two connections. A statement whose trees do not fit is refused with `ERR_PAYLOAD_TOO_LARGE`.
- **Elevation of privilege:** the statement-kind gate rejects writes (INSERT/UPDATE/DELETE/MERGE) with `ACL_REJECTED` and DDL/COPY/ATTACH/LOAD/INSTALL with `ERR_UNSUPPORTED_SYNTAX` — DuckDB never sees a mutation from a read-only SqlAccess policy. The function gate rejects every function call that is not on a reviewed allowlist with `ACL_REJECTED`. Policy and the rewrite passes key on table references, so a function such as `query_table`, `read_csv`, `duckdb_settings` or `current_setting` would read tables, files or settings without access check, row filter or mask. In FROM only `generate_series`, `range` and `unnest` are allowed, and their arguments must not contain column references or subqueries. Outside FROM only functions that compute over their arguments are allowed (`internal/parser/functions_allow.go`). `TABLESAMPLE`, `XMLTABLE`, `JSON_TABLE`, a table name that contains path characters (`/ \ : * ? [`, DuckDB would open it as a file or URL), and qualified function names (`schema.f(x)` and the method form `x.f()`) are rejected too. The checks run on the pg_query tree, so the parser refuses unquoted identifiers that contain non-letter Unicode characters: DuckDB reads Unicode spaces as separators and would see different tokens in a statement that is served unchanged. The query service runs the same check before the rewrite cache, so a memoised allow cannot bypass it, and audits the request as `reject`.
- **Elevation of privilege (wrapper alias):** a filtered or masked table without an alias gets a wrapper named after the table, which pg_query deparses bare when it has no upper case. DuckDB reads some names as a keyword there: `semi`, `anti`, `asof` and `positional` change the join type and drop the wrapper's name, and others break the statement. The rewriter refuses such a table with `ERR_UNSUPPORTED_SYNTAX` and asks for an alias. The parser refuses the same words as a FROM alias the user writes (`ErrAliasKeyword`, `ERR_SYNTAX`): pg_query writes `generate_series(1, 1) AS qualify(x)` bare, and DuckDB reads it as `QUALIFY (x)`, a row predicate on the raw value of a masked column or of the select alias of a post-masked item. The word list (`parser.IsUnsafeBareAlias`) is checked against `duckdb_keywords()` by `TestWrapperAliasKeywords`, which must be re-run on every DuckDB or pg_query upgrade.

### 4.4 Executor (`internal/executor`)

- **Tampering:** hardening disables external file access (`external_access=false`), blocks community extensions, disables autoinstall / autoload, disables persistent secrets (applied at database-open time via the connector DSN, since httpfs initializes the secret manager during attach), and locks configuration after init. `AttachHook` runs first (extensions need external access to install), then hardening, then `lock_configuration=true`. When `s3`/`http` sources are configured, their allow-listed locations are carved out via DuckDB `allowed_directories`/`allowed_paths` before the floor — query-time reads succeed only for the configured bucket prefixes and exact URLs; every other URL and the local filesystem stay blocked. A user query cannot read a file in a carve-out directly, neither with `read_csv('s3://allowed-bucket/…')` nor with the path or URL as a quoted table name (table names with path characters are refused). Such a read would return the file without the row filters and column masks defined on the view over it, so the carve-out is not a policy boundary. Both forms are rejected by the function gate (see 4.3).
- **Information disclosure:** DuckDB does not have a secret-leak surface once `allow_persistent_secrets=false` and external access are off. Driver-created secrets via `CREATE SECRET` are connection-scoped; the http driver scopes its header secrets to each URL origin so credentials never travel to other hosts.
- **Denial of service:** per-request `context.WithTimeout` + pool sizing (`MaxOpen=4`, `MaxIdle=2`). A hostile query hitting the timeout surfaces as `CodeTimeout` and is audited.
- **Elevation of privilege:** the empty ATTACH URL + named-secret pattern used by the postgres/mysql drivers keeps credentials out of DuckDB's query log and crash dumps.

### 4.5 Identity (`internal/identity`)

- **Spoofing:** API-keys verified via HMAC-SHA256 against a server-wide pepper stored in `secret://`. JWTs verified against a JWKS per issuer; `alg: none` is explicitly rejected; algorithm allowlist is HS/RS/ES 256 / 384.
- **Tampering:** JWKS cache has a per-URL mutex that collapses concurrent unknown-kid fetches into one request, and a 30 s rate limit between repeat unknown-kid refreshes — a hostile client cannot force unbounded fetches by rotating the kid header. Stale-serve protects against transient IdP outages.
- **Repudiation:** every authenticated request carries a `UserCtx` that is recorded in the audit record (subject, issuer, email, groups, request-id, remote-addr).
- **Information disclosure:** API-key failures add bounded random jitter so unknown-key-id is indistinguishable from bad-HMAC by response time. `WWW-Authenticate: Bearer realm="sluice"` on 401 doesn't reveal which method would have worked.
- **Denial of service:** the identity middleware is O(1) per request; JWKS cache refresh is bounded (see tampering).
- **Elevation of privilege:** `AllowAnonymous` only applies to `ErrNoCredential`. A presented-but-invalid credential always 401s; there is no fall-through path where a bad credential is quietly downgraded to anonymous.

### 4.6 Audit (`internal/audit`)

- **Tampering:** the hash chain (`hash_i = sha256(hash_{i-1} || "\n" || canonical_i)`) makes any single-record or mid-chain modification detectable. `VerifyError.Is(ErrChainBroken)` is the standard way callers detect tampering.
- **Spoofing:** the genesis seed anchors the chain. A replay attacker with a different seed produces a chain whose first record's `prior_hash` mismatches what the verifier expects. Operators should store the seed in `secret://` and rotate only at a formal boundary.
- **Repudiation:** every terminal outcome emits exactly one record; the wrapped `RowIterator` emits on Close with the final row count.
- **Information disclosure:** records contain the rewritten SQL, the parameter values, the applied policies, and the caller's identity. The file sink enforces mode `0640` and the directory must be access-controlled by the operator.
- **Denial of service:** bounded dispatch queue (default 10 000) with non-blocking-then-deadlined send; when the queue is full `Enqueue` returns `ErrQueueFull` and increments `sluice_audit_dropped_total`. The executor still runs; the deny is the conservative choice for integrity-critical sinks, the drop-with-metric is safer for availability-critical deployments (configurable per deployment).

### 4.7 REST transport (`internal/transport/rest`)

- **Tampering / spoofing:** TLS termination is the operator's responsibility. Sluice validates identity per request; middleware order (body cap → timeout → panic recovery → identity) ensures the body cap takes effect before any parser work.
- **Information disclosure:** errors are rendered as `APIError` JSON with a canonical HTTP status; `X-Query-Id` is emitted on both success and error so operators can correlate a report with an audit record.
- **Denial of service:** body cap (1 MiB default), per-request timeout, panic recovery.

### 4.8 MCP transport (`internal/transport/mcp`)

Same threat profile as REST plus: MCP errors are returned as `CallToolResult{IsError: true}` so LLM clients can reason about them. This is a deliberate choice — a protocol-level error would break the agent; a tool-result error is self-describing and leaves the session alive.

### 4.9 Admin transport (`internal/transport/admin`)

- **Spoofing:** static-token auth with constant-time compare. Empty token logs a loud boot warning. Operators should bind the admin listener to a private interface.
- **Elevation of privilege:** read-only in MVP — no endpoint mutates state except `POST /admin/reload` which re-reads the on-disk config directory. The reloader trips the same code path as fsnotify / SIGHUP.
- **Information disclosure:** `/admin/subjects/explain` reveals the policy decision for an arbitrary subject; this is intentional for operator use and is gated by the admin token.

### 4.10 Configuration and secrets

- `secret://<provider>/<path>` resolution caches with per-entry TTL and makes defensive copies on get/set — a caller that mutates a returned slice cannot poison subsequent lookups.
- The `file://` provider rejects group/world-writable secret files (perm mask 0o022) and warns on world-readable (0o004).
- The audit genesis seed should be stored separately from the audit log itself so a disk-level attacker cannot rewrite both.

### 4.11 Datasource drivers (`internal/datasource/drivers/*`)

- Each driver validates identifiers against `[A-Za-z_][A-Za-z0-9_$]*` before interpolating catalog names into `CREATE SECRET` / `ATTACH` statements.
- `CREATE OR REPLACE SECRET` uses a named secret + empty ATTACH URL so passwords never appear in DuckDB's query log.
- `s3parquet`'s `PathAllowed` glob enforces the `allowedPaths` whitelist — `read_parquet('<path>')` views are only created for paths that match.
- `motherduck.SET motherduck_token` is connection-local; the token is released when the `*sql.Conn` returns to the pool.

## 5. Attack surface inventory

| Surface                               | Default exposure             | Notes                                             |
|---------------------------------------|------------------------------|---------------------------------------------------|
| REST `/v1/query`                      | Operator-controlled network  | Identity required; body cap; timeout.             |
| REST `/v1/health`, `/v1/ready`        | Operator-controlled network  | No identity; returns liveness only.               |
| REST `/v1/version`                    | Operator-controlled network  | No identity; returns build identity.              |
| REST `/openapi.json`                  | Operator-controlled network  | Static stub in MVP.                               |
| MCP stdio                             | Local process                | Parent process supplies the identity context.     |
| MCP Streamable HTTP                   | Operator-controlled network  | Identity bridged into request context.            |
| Admin `/admin/*`                      | Private network only         | Static token; constant-time compare.              |
| Prometheus `/metrics`                 | Private network only         | Plaintext metric exposure; no identity by design. |
| Config + policy directory             | Filesystem                   | Operator-owned; watched via fsnotify.             |
| Audit directory                       | Filesystem                   | Operator-owned; should be append-only.            |

## 6. Mitigations summary

- **Default-deny.** Empty or missing policy directory rejects every query. Invariant tested on every relevant slice.
- **Parameterised rewriting.** Templates render to positional `$N`; no user data flows into SQL literals.
- **Function allowlist.** Function calls are default-deny in FROM and in expressions. A DuckDB upgrade cannot add a callable function without a review of the allowlist.
- **Hardened executor.** External file access off, community extensions off, configuration locked after init.
- **Hash-chained audit.** Tamper-evident by construction; genesis-seed anchor prevents replay from a different installation.
- **Signed-and-pinned dependencies.** `go.sum` checked in; `go mod tidy` is CI-enforced; `govulncheck` runs on every PR.
- **SPDX headers + license separation.** Every source file carries a header; `scripts/check-spdx.sh` enforces in CI; `pkg/` cannot import `internal/` (depguard).
- **Constant-time comparisons** for admin token and API-key HMAC.
- **Bounded fetches** for JWKS (per-URL mutex + rate limit) and for audit dispatch (bounded channel).
- **Release artifact integrity.** cosign-signed binaries + multi-arch Docker images with syft SBOM and SLSA provenance (landing with the release matrix).

## 7. Known limitations

- **What a mask output shows.** For a SQL mask no expression of the query sees the raw value, only the mask output (§4.3), so `WHERE email = 'victim@example.com'` under a `'***'` mask matches nothing. The output itself can still tell something: a `partial` mask shows its prefix and suffix, an unsalted `sha256` hash can be checked against guessed values, and equal hashes or `fpe` values show equal inputs. Choose the mask for the data, and use `hmac_sha256` for low-entropy values.
- **CEL is declared-only.** `SqlAccessPolicy.condition` and `QueryRejectPolicy.rules[].when` parse today but do not evaluate. A policy that relied on a CEL condition to deny would currently fall through; the engine rejects unknown CEL at load time to make this visible. CEL GA lands in v0.2.
- **No write path.** MVP is read-only. `WritableDataSource` is declared but not implemented; operators who need write-through must run a separate gateway.
- **Single-instance audit chain.** Hash-chaining within one instance is straightforward; horizontal scaling requires an external ordering guarantee (Kafka, Kinesis, etc.) and is deferred to v1.
- **No OIDC discovery.** JWKS URLs must be configured explicitly per issuer in MVP; OIDC `/.well-known/openid-configuration` auto-discovery lands in v0.3.
- **The DuckDB parse check has blind spots.** It runs only on the mutation path and only for `SELECT`. Pass-through runs the caller's text as written, while policy reads the pg_query tree: `0x1f` is 31 for pg_query and `0 AS x1f` for DuckDB. `EXPLAIN`, `SET` and `SHOW` are not checked (`json_serialize_sql` serializes `SELECT` only). The check compares the text before mutation, so it does not cover name resolution, the fragments the mutators add (row filter predicate, mask expressions, star expansion, wrapper alias) or the sampling wrap. Those are a closed vocabulary pinned by tests (`internal/executor/filter_sql_test.go`, `mask_sql_test.go`, the differential tests). The serializer drops type catalogs: `'12'::pg_catalog.int4` is served, though DuckDB refuses the raw text at bind. Names and aliases are compared without regard to ASCII case. The normalizer gives an alias the caller's spelling back, but a name written in two spellings stays lower case, and that shows in result column names, struct keys and `to_json` keys. A keyword, a type name or an `EXTRACT` unit counts as such a spelling: `EXTRACT(MONTH FROM d) AS Month`, `ts::date AS Date` and `EXTRACT(EPOCH FROM ts) AS Epoch` give `month`, `date` and `epoch`. A struct field read without an alias loses its spelling too: DuckDB names the column of `SELECT st.Y` after the field as written (`Y`), and the served `st.y` gives `y`. The generated column name of an unaliased expression the normalizer changed is not DuckDB's name for the raw text: `SELECT ts + INTERVAL '3' DAY` gets a name with `"DOUBLE"` where the raw text gives `DOUBLE`. Some valid forms are refused, such as `EXTRACT(days FROM d)`, `ARRAY[[1, 2]]` and parentheses the deparse drops; [docs/policies/row-filters.md](docs/policies/row-filters.md) lists them with what to write instead. The normalizers and the comparator depend on the exact DuckDB and pg_query versions: re-run `go test ./internal/rewriter -run Differential` (without `-short`) on every bump of `scripts/DUCKDB_VERSION` or pg_query_go.
- **MotherDuck live integration is best-effort.** The driver is wired and unit-tested, but live round-trips depend on a nightly lane gated on a token.

## 8. Residual risk

Even with every mitigation above, a sufficiently privileged insider (direct filesystem access, ability to rotate the genesis seed, ability to push a signed release) can undermine the guarantees. Sluice is a control plane; it cannot substitute for the operator's host-, network-, and supply-chain hygiene.

The explicit residual risks we acknowledge:

1. An insider who can both rotate the audit genesis seed and replace the audit log can produce a consistent but forged chain. Mitigation: store the seed in a separate KMS; back the audit directory with a WORM volume.
2. A vulnerability in DuckDB, pg_query, or a driver's upstream extension — Sluice pins versions, monitors `govulncheck`, and patches promptly, but the fix window is owned by upstream.
3. Policy misconfiguration. Sluice validates structure, but semantic correctness (does this policy actually match your compliance requirements?) is the operator's responsibility. `policy explain` and the golden fixture harness are aimed at this.

## 9. Security contact

- `security@bino.bi` for reports.
- `conduct@bino.bi` for code-of-conduct matters.
- GitHub private vulnerability reporting on `bino-bi/sluice`.

See `SECURITY.md` for the full disclosure process and SLA.

---

Last reviewed: 2026-10-11. This document is amended by PR; material changes go through the RFC process in [GOVERNANCE.md](GOVERNANCE.md).
