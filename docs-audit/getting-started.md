# Audit: Home and Getting started

Audited at commit 6f7b99f with `/tmp/sluice-bin/sluice` (`sluice dev (6f7b99f, dirty=false)`).
Live checks ran against a scratch copy of `examples/hello-sluice` (SQLite seeded with python3, REST on 127.0.0.1:18100, admin on 18101). Docker, image pulls, release downloads and cosign were not available, so those blocks are `not verified`.

## Summary

### Not implemented

- docs/index.md:52 (also install.md:124): `AuditSink` as a working policy kind. The kind decodes, but nothing reads `Snapshot.AuditSinks` (set at internal/config/policies.go:131). Sinks are built from server config only (cmd/sluice/auditwire.go:28). Types s3, syslog, postgres and otlp are refused at validate time (pkg/apitypes/decoder.go:516-521). A `type: file` manifest loads and has no effect.
- docs/getting-started/first-query.md:127: a visible result of `enforcementMode: Audit`. The match is stored in `Decision.Shadow` (internal/policy/decision.go:35), but no code outside internal/policy reads it. Looked for it in the audit record, the response headers and `policy explain` output: not there. Today `Audit` behaves like "off". docs/policies/index.md:91 says a shadow match is recorded.

### Docs and code disagree

- All five pages: docs name `v0.1.0-rc.2` as the release to install, but describe `main`. Five commits after the tag changed behavior shown here (`git diff v0.1.0-rc.2 HEAD`): `params` (rc.2 docs: supported, now refused), rewritten SQL shape (`sluice_src` alias, mask subquery), Go 1.25 to 1.26. A reader on the rc.2 image gets different results. Only the HEAD build was tested.
- docs/index.md:62: doc "6 datasource drivers: Postgres, MySQL, SQLite, S3 Parquet, DuckDB file, MotherDuck". Code: 7 drivers, `http` is missing from the list (pkg/apitypes/datasource.go:58-64, cmd/sluice/bootstrap.go:17-23). `s3` reads parquet, csv, json and xlsx (pkg/apitypes/datasource.go:70-73).
- docs/index.md:52: doc "11 policy kinds". Code: 11 kinds decode (pkg/apitypes/typemeta.go:21-31), 10 are wired, `AuditSink` is not (see above).
- docs/index.md:5: doc "Each request, allowed or denied, lands in a hash-chained audit log". Code: only requests that reach the query pipeline are audited. Run: a 401 (no key), a 400 bad JSON body and a 400 `format: arrow` left no audit record (handler returns before `Service.Execute`, internal/transport/rest/query.go:29-46).
- docs/getting-started/index.md:21 and quickstart.md:9: doc "docker-compose.yaml builds the image from the repository root". Code: compose pulls `ghcr.io/bino-bi/sluice:0.1.0-rc.2`, there is no `build:` key (examples/hello-sluice/docker-compose.yaml:10). quickstart.md:120 says it correctly.
- docs/getting-started/quickstart.md:18: doc lists `data/` as part of the example. Repo: `examples/hello-sluice/data/` is not tracked (only ignore rules, examples/hello-sluice/.gitignore:3-5).
- docs/getting-started/install.md:123: doc "Any setting can also come from the environment with the `SLUICE_` prefix". Code: an env var is read only when its key has a registered default (66 keys, internal/config/server.go:420-495) or is present in the config file (viper `AutomaticEnv`, internal/config/server.go:387-389). Run: `SLUICE_REST__LISTEN` always works. `SLUICE_ADMIN__TOKEN` and `SLUICE_AUDIT__FILE__PATH` are silently ignored when the file does not hold those keys, and work when it does. Keys without a default: `admin.token`, `identity.apiKeyPepper`, `audit.file.*`, `audit.syslog.*`, `audit.s3.*`, `duckdb.tempDir`, `mcp.listen`, `rest.tls.*`, `admin.tls.*`, `approval.webhooks`.
- docs/getting-started/install.md:124: doc says everything in `policies.d/` (policies, `DataSource`, `SubjectBinding`, `AuditSink`) is hot-reloaded. Code: `DataSource` attachments are built once at boot (cmd/sluice/bootstrap.go:155), a change needs a restart (internal/config/validate.go:36-38, docs/operations/hot-reload.md:87-88). `AuditSink` is not wired.
- docs/getting-started/install.md:91: doc "`make libduckdb` needs cmake + ninja + python3". Script: also git, a C++ toolchain, curl, zip, unzip, tar and pkg-config (scripts/build-libduckdb.sh:40-42).
- docs/getting-started/first-query.md:20: doc "Two headers accompany every response: `X-Query-Id` and `X-Sluice-Applied-Policies`". Code: `X-Sluice-Applied-Policies` is set only on a 200 with at least one applied policy (internal/transport/rest/query.go:65-68). `X-Query-Id` is set only when a query id exists (internal/transport/rest/errors.go:33-35). Run: a 403 deny has `X-Query-Id` only, a 400 arrow and a 401 have neither.

### Broken code blocks

- docs/getting-started/install.md:111 and quickstart.md:23: `sqlite3 data/shop.db < seed.sql` fails on a fresh checkout. `data/` does not exist and SQLite does not create parent directories. Checked on a `git archive` copy with python3 sqlite3: `unable to open database file` (the sqlite3 CLI is not installed here). Fix: `mkdir -p data` first.
- Works only under a condition the page does not state: quickstart.md:148, first-query.md:104 and first-query.md:136 use paths from the repo root (`examples/hello-sluice/...`). quickstart.md:24 moved the reader into `examples/hello-sluice`, and first-query.md:54 edits `policies.d/mask-email.yaml` relative to that directory. Run from there: `policy validate` and `config validate` print `policies OK ... (0 objects, digest e3b0c44298fc)` with exit 0 (a missing directory counts as empty), `policy explain` prints `decision: deny`, `audit verify` exits 1. From the repo root all three match the docs.
- Not runnable on the Docker path: the same three blocks and quickstart.md:82 call a local `sluice` binary. A reader who took the Docker path has none. The pages do not show `docker compose exec`.
- Not verified, likely to fail with Docker on Linux: the container runs as uid 65532 (Dockerfile.release:27) and must create `/data/audit` inside the host bind mount `./data`. The server creates the audit dir 0750 and files 0640, so `sluice audit verify` on the host may not be able to read them. Needs a real Docker run.

### Proposed pages for this section

Home stays. Getting started goes from 4 pages to 2. The path is short (install, run, one query). The current index page (222 words) only lists chapters and repeats the quickstart intro. Half of first-query repeats the REST and policy reference.

1. `docs/index.md` (Home): one paragraph on what Sluice is, the pipeline diagram, the one-policy example, a short feature list without counts, where-to-start links, status note. About 200 words.
2. `docs/getting-started/index.md` (Quickstart, the section landing page): prerequisites, run `hello-sluice` (Docker or local binary, with `mkdir -p data` and one fixed working directory), first query with real response, one denied query, change the mask to `partial`, `policy explain`, `audit verify`. About 450 words.
3. `docs/getting-started/install.md` (Install): image, release archive with checksum and cosign check, build from source. About 300 words. Move static extensions and "build the image yourself" to `operations/deployment.md`, which already owns them. Move "Directory conventions" to `operations/server-config.md`.

Remove `getting-started/quickstart.md`: merge into the landing page. Reason: the landing page has no content of its own.
Remove `getting-started/first-query.md`: the mask edit, `policy explain` and `audit verify` steps become the last steps of the quickstart. The request table goes to `reference/rest-api.md` (already there, lines 28-80). `enforcementMode` goes to `policies/index.md`.

### Features the code has that the section does not document

- `Authorization: ApiKey <key>` works as well as `X-Api-Key` (internal/identity/apikey.go:187-200).
- `GET /v1/health`, `/v1/ready`, `/v1/version` to check the server is up and which build runs (internal/transport/rest/routes.go:22-24). `/v1/version` returns the commit.
- Truncated results carry a `warning` object (`ERR_RESULT_TRUNCATED`) in the JSON body (pkg/apitypes/dto.go:35-37). Seen with `max_rows: 1`.
- `X-Request-Id` is set on every REST response (internal/transport/rest/routes.go:66-75).
- `sluice policy explain --sql "<statement>"` (cmd/sluice/policy.go:231) and `--strict` on both validate commands (cmd/sluice/policy.go:85).
- `sluice policy test` prints the rewritten SQL on a failed `rewrittenSql` expectation. It is the only user-facing way to see the rewritten statement (internal/policytest/policytest.go:321-322). The audit record holds only `rewritten_fingerprint`.
- `sluice version --json` (cmd/sluice/version.go:36).

## Diagrams

- docs/index.md:7-9 (ASCII pipeline): no label is proven wrong. Order in code: identify (auth middleware, internal/transport/rest/routes.go:35), parse (internal/queryservice/execute.go:107), policy (:177), rewrite (:281), execute (:326), audit access record before any row (:386-395), rows (:397).

## docs/index.md

Prose words now: 328. Suggested after rewrite: 200.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5 | SQL gateway between agents, BI tools and databases, over REST and MCP | implemented (internal/transport/rest/routes.go:35) | MCP: internal/transport/mcp/tools.go:23. REST run, MCP read only |
| 2 | 5 | Statements parsed with the PostgreSQL grammar (pg_query) | implemented (internal/queryservice/execute.go:110) | go.mod:19, `sluice version` prints `parser=pg_query` |
| 3 | 5 | Default-deny YAML policies | implemented (internal/policy/conflict.go:150) | Run: no matching allow gives 403 `ACL_DENIED` |
| 4 | 5 | Rewritten in flight: row filters, column masks, LIMIT and sampling clamps | implemented (internal/queryservice/execute.go:281-287) | Clamps: internal/rewriter/apply_rewrite.go:20-58. Filter and mask run, clamps read only |
| 5 | 5 | Executed read-only through embedded DuckDB on attached catalogs | implemented (internal/rewriter/rewriter.go:360) | `READ_ONLY` attach, internal/datasource/drivers/sqlitefile/driver.go:106. Run: `DELETE` gives 403 `ACL_REJECTED` |
| 6 | 5 | Each request, allowed or denied, lands in a hash-chained audit log, verifiable offline | mismatch | Only requests that reach the pipeline. Run: 401, bad JSON and `format: arrow` left no record (internal/transport/rest/query.go:29-46). Verify works (cmd/sluice/audit.go:75) |
| 7 | 13 | A `RowFilterPolicy` scopes matching queries to the caller's tenant | implemented (internal/policy/conflict.go:236-238) | Run: 2 of 5 customers returned |
| 8 | 45-48 | Rewritten SQL shape, `$1` bound as a positional parameter | implemented (internal/queryservice/execute.go:329-330) | Exact string confirmed with `sluice policy test` (allow policy plus this filter). Same shape in internal/rewriter/wrap_test.go:234 |
| 9 | 52 | 11 policy kinds | mismatch | Doc: 11. Code: 11 decode (pkg/apitypes/typemeta.go:21-31), 10 have an effect. Count goes stale |
| 9a | 52 | `AuditSink` is one of the usable kinds | not implemented | Looked for a reader of `Snapshot.AuditSinks` (internal/config/policies.go:131): none. Sinks come from server config (cmd/sluice/auditwire.go:28). s3, syslog, postgres, otlp types are refused (pkg/apitypes/decoder.go:516-521) |
| 10 | 53 | Mask catalog: SQL-stage null, constant, partial, hash, regex, truncate. Post-query HMAC, FPE (FF1), jitter, fake | implemented (pkg/mask/registry.go:85-96) | HMAC is post-query: pkg/mask/hash.go:67. FF1: pkg/mask/fpe.go:80. Only null and partial run |
| 11 | 54 | CEL for conditions, reject rules and a row-filter subset | implemented (internal/policy/cel.go:32) | Row-filter env: internal/policy/cel.go:46, lowering: internal/policy/cel_sql.go:30. Read only |
| 12 | 55 | Approval workflow: park queries, webhooks, single-use grants | implemented (internal/queryservice/execute.go:251-278) | internal/approval/broker.go:192 and :330. Read only |
| 13 | 56 | Embedded OPA and OpenFGA ReBAC compose with the YAML engine | implemented (cmd/sluice/policyengine.go:27-43) | Members: cmd/sluice/policyengine.go:63-74. Read only. Stale comment at :51 says only YAML exists |
| 14 | 57 | Per-subject token buckets, daily CPU-seconds and rows quotas | implemented (internal/queryservice/execute.go:57-58) | internal/ratelimit/limiter.go:123, internal/budget/manager.go:25 and :137-141. Read only |
| 15 | 58 | Hot reload: file watcher, SIGHUP, `POST /admin/reload`. Invalid snapshots never replace a good one | implemented (cmd/sluice/serve.go:58-64) | internal/transport/admin/routes.go:21. All three run. Bad policy: log `reload failed`, old policies still served, admin reload returns `ERR_POLICY_INVALID` |
| 16 | 59 | `sluice policy test` runs fixture suites | implemented (cmd/sluice/policytest.go:32) | Run. "in CI" is advice: no workflow in this repo calls it |
| 17 | 60 | Hash-chained JSONL audit, `sluice audit verify` | implemented (cmd/sluice/audit.go:75) | internal/audit/file_sink.go:25. Run: `chain OK (1 file(s), 23 record(s), ...)` |
| 18 | 61 | MCP server with 9 tools over stdio or Streamable HTTP | implemented (internal/transport/mcp/tools.go:25-81) | Transport flag: cmd/sluice/mcp.go:133. Read only. Count goes stale |
| 19 | 62 | 6 datasource drivers | mismatch | 7 registered, `http` missing (cmd/sluice/bootstrap.go:17-23). `s3` is not Parquet only (pkg/apitypes/datasource.go:70-73) |
| 20 | 66-71 | "Where to start" table | fluff | Navigation. Keep as plain links |
| 21 | 74 | Current release is `v0.1.0-rc.2`, pin an exact version | duplicate | Owner: getting-started/install.md. See the release note in the summary |
| 22 | 74 | Binaries and multi-arch images published on GitHub Releases and ghcr.io | implemented (.goreleaser.yaml:162-166) | Archives: .goreleaser.yaml:84-89. Config only, registry not reachable from here |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 7 | text | Pipeline diagram | untested | not verified: a diagram, not runnable. Labels checked, see Diagrams |
| 15 | yaml | `RowFilterPolicy` filter-tenant | tested | Same content as examples/hello-sluice/policies.d/filter-tenant.yaml, loaded by `newHelloSluiceService` (internal/queryservice/function_gate_duckdb_test.go:109). Also passes `policy validate` |
| 38 | sql | Client query | untested | verified by hand on the scratch server |
| 44 | sql | Rewritten query | untested | verified by hand: `sluice policy test` with `rewrittenSql` set to this string passes |

## docs/getting-started/index.md

Prose words now: 222. Suggested after rewrite: 450 (becomes the quickstart, absorbs quickstart.md and the steps of first-query.md).

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5 | Takes about ten minutes | fluff | Not measurable |
| 2 | 7 | SQLite catalog `shop` with customers and orders for three tenants | implemented (examples/hello-sluice/seed.sql:33-46) | DataSource: examples/hello-sluice/policies.d/datasource-shop.yaml:5-8 |
| 3 | 8 | API-key `SubjectBinding` identifies the caller and stamps a tenant | implemented (cmd/sluice/bootstrap.go:774-781) | tenantId: cmd/sluice/bootstrap.go:795-796. Run |
| 4 | 9 | One `SqlAccessPolicy` grants exactly two tables | implemented (examples/hello-sluice/policies.d/allow-analytics.yaml:16) | Run: `admin_users` denied |
| 5 | 10-11 | Row filter scopes to the tenant, column mask hides `email` | implemented (examples/hello-sluice/policies.d/mask-email.yaml:15-16) | Run |
| 6 | 12-14 | One curl shows all of it. Everything else "falls out of those five files" | fluff | The audit trail comes from `server.yaml`, not from the five policy files |
| 7 | 20 | Go 1.26 or later plus a C toolchain, cgo needed | duplicate | Owner: getting-started/install.md. go.mod:3 |
| 8 | 21 | Compose file builds the image from the repository root and wires all secrets | mismatch | Doc: builds. Code: pulls `ghcr.io/bino-bi/sluice:0.1.0-rc.2`, no `build:` key (examples/hello-sluice/docker-compose.yaml:10). Secrets part is right (:17-22) |
| 9 | 23 | `sqlite3` and `curl` are needed | implemented (examples/hello-sluice/seed.sql:3) | Also needs `mkdir -p data`, see broken blocks |
| 10 | 27-29 | Chapter list | fluff | Repeats the nav |

### Code blocks

None.

## docs/getting-started/install.md

Prose words now: 717. Suggested after rewrite: 300.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 6 | Current release is `v0.1.0-rc.2`. API and policy schema may change. Pin an exact version | implemented (CHANGELOG.md:75) | Tag exists. Value goes stale. The pages describe `main`, not rc.2, see summary |
| 2 | 6 | No Helm chart | implemented (.goreleaser.yaml:84) | True: no chart in the repo, release ships archives and images only |
| 3 | 10 | Image is multi-arch (`linux/amd64`, `linux/arm64`), distroless, non-root | implemented (.goreleaser.yaml:116-166) | Dockerfile.release:16 and :27 |
| 4 | 17 | Both images attach all datasource types offline. amd64 has 3 extensions compiled in and pre-seeds the rest, arm64 pre-seeds all six | implemented (.github/workflows/release.yaml:92-97) | .goreleaser.yaml:62-65, Dockerfile.release:25, load path: internal/datasource/drivers/common/extensions.go:35. Not run |
| 5 | 19 | Image tag has no leading `v`. Pre-releases never get `latest` | implemented (.goreleaser.yaml:163) | `latest` only when not a pre-release: .goreleaser.yaml:124 and :169 |
| 6 | 23 | Archive targets: linux_amd64, linux_arm64, darwin_amd64, darwin_arm64 | implemented (.goreleaser.yaml:42-46) | linux_amd64: .goreleaser.yaml:72-73. Name template: .goreleaser.yaml:88-89 |
| 7 | 26 | No Windows archive. Native build works and CI covers it | implemented (.goreleaser.yaml:36-42) | CI is build-only: .github/workflows/test.yaml:26-28. Stale header comment at .goreleaser.yaml:4-7 still lists windows |
| 8 | 26 | Why: DuckDB Windows libraries and `pthread_*` symbols under mingw | internal detail | Reader only needs "build from source on Windows" |
| 9 | 35 | linux_amd64 archive is the static-extension build, others install extensions at first attach | implemented (.goreleaser.yaml:57-65) | .goreleaser.yaml:25-27 |
| 10 | 38 | Linux archives need glibc 2.38 and GLIBCXX_3.4.32. List of distros that work or fail | implemented (Dockerfile.release:10-13) | Evidence is a comment plus the pinned build image (.github/workflows/release.yaml:28) and CHANGELOG.md:81. Distro list not checkable here |
| 11 | 42 | `checksums.txt` signed keyless with cosign, CycloneDX SBOM per archive, SLSA provenance `multiple.intoto.jsonl` | implemented (.goreleaser.yaml:182-194) | Checksum: .goreleaser.yaml:100-102. SBOM: :174-180. SLSA: .github/workflows/release.yaml:138-154. "v1.0" comes from a comment |
| 12 | 60 | Container manifests are signed | implemented (.goreleaser.yaml:196-204) | |
| 13 | 72-73 | Go 1.26 or later. C toolchain. Cannot build with `CGO_ENABLED=0` | implemented (go.mod:3) | Run: `CGO_ENABLED=0 go build ./cmd/sluice` fails, "build constraints exclude all Go files" in duckdb-go-bindings |
| 14 | 82 | `make build` writes `./bin/sluice` with version ldflags. `sluice version` prints tag, commit, build time, parser backend | implemented (Makefile:50-52) | ldflags: Makefile:36-42. Format: internal/version/version.go:119-129. `sluice version` run. It also prints dirty flag, Go version and platform |
| 15 | 82 | `make all` runs fmt, vet, lint, test, build | implemented (Makefile:48) | Not run. `lint` needs golangci-lint |
| 16 | 84 | First build compiles pg_query C sources and takes several minutes | internal detail | Not measured |
| 17 | 88 | A plain build downloads DuckDB extensions from `extensions.duckdb.org` on first attach | implemented (internal/datasource/drivers/common/extensions.go:35-41) | `LOAD` first, `INSTALL` only if that fails |
| 18 | 88 | Static target compiles in httpfs, excel, sqlite_scanner. postgres, mysql and motherduck cannot be embedded | implemented (Makefile:62-65) | scripts/build-libduckdb.sh:109. "always install at attach" is loose: a pre-seeded file is loaded without a download |
| 19 | 91 | `make libduckdb`: 20 to 40 min, needs cmake, ninja, python3, cached under `third_party/` | mismatch | Doc lists 3 tools. Script also needs git, C++ toolchain, curl, zip, unzip, tar, pkg-config (scripts/build-libduckdb.sh:40-42). Time and cache are right (Makefile:21 and :27) |
| 20 | 95 | `make test-static` runs the tests plus a static-linking smoke test | implemented (Makefile:78-79) | internal/executor/static_extensions_test.go:3. Not run |
| 21 | 95 | DuckDB version pinned in `scripts/DUCKDB_VERSION`, checked against go.mod | internal detail | True (scripts/build-libduckdb.sh:49-59) |
| 22 | 99 | Root `Dockerfile` is multi-stage and gives the same layout as the published image | duplicate | Owner: operations/deployment.md:55-63. True (Dockerfile:21, :54, :96, :113). A fourth stage `extensions` exists (Dockerfile:45). Base is debian12, published image is debian13 |
| 23 | 105 | Every example compose file takes a `SLUICE_IMAGE` override | duplicate | Owner: operations/deployment.md:77. True for all 5 compose files, e.g. examples/hello-sluice/docker-compose.yaml:10 |
| 24 | 109 | `hello-sluice` compose pulls the published image and mounts config, policies and data | duplicate | Owner: the quickstart. True (examples/hello-sluice/docker-compose.yaml:10 and :23-26) |
| 25 | 117 | REST on `:8080`, admin on `:9091`. No Go toolchain, no build step | implemented (examples/hello-sluice/server.yaml:5) | Admin: examples/hello-sluice/server.yaml:11. Ports: docker-compose.yaml:13-14 |
| 26 | 123 | `sluice.yaml` is the server config, passed with `sluice serve --config` | implemented (cmd/sluice/serve.go:131) | Run |
| 27 | 123 | A missing config file is fine, a malformed one is fatal | implemented (internal/config/server.go:391-401) | Run with `config validate`: missing path prints `server config OK`, exit 0. Malformed: exit 1 |
| 28 | 123 | Any setting can come from env with `SLUICE_` and `__` | mismatch | Only keys with a registered default (internal/config/server.go:420-495) or present in the config file. Run: `SLUICE_ADMIN__TOKEN` and `SLUICE_AUDIT__FILE__PATH` are ignored unless the file holds the key. `SLUICE_REST__LISTEN` always works |
| 29 | 124 | `policies.d/` is loaded recursively, `*.yaml` and `*.yml`, skips dot-directories, `testdata`, `tests`. Set with `policies.directory` or `--policies-dir` | implemented (internal/config/policies.go:167-197) | Skip rule: :176. Flag: cmd/sluice/serve.go:132. Config key: cmd/sluice/bootstrap.go:132 |
| 30 | 124 | One directory for policies, `DataSource`, `SubjectBinding`, `AuditSink`, hot-reloaded on change | mismatch | Policies and bindings reload (run). `DataSource` changes need a restart (cmd/sluice/bootstrap.go:155, internal/config/validate.go:36-38). `AuditSink` is not wired |
| 31 | 127 | An empty `policies.d/` is valid and means deny everything | implemented (internal/config/policies.go:66-67) | Run: `policy validate` on an empty dir gives `0 objects`, exit 0 |
| 32 | 119-127 | Whole "Directory conventions" section | duplicate | Owner: operations/server-config.md |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 12 | bash | `docker pull` and `docker run ... version` | untested | not verified: no Docker, no registry access. Image name matches .goreleaser.yaml:163, entrypoint Dockerfile.release:32 |
| 28 | bash | Download and unpack the release archive | untested | not verified: no download made. File name matches .goreleaser.yaml:88-89 |
| 44 | bash | Checksum and `cosign verify-blob` | untested | not verified: no network, no cosign. Same command as the release footer (.goreleaser.yaml:232-237). Needs `VERSION` from the block at line 28 |
| 62 | bash | `cosign verify` for the image | untested | not verified: no network, no cosign |
| 75 | bash | `git clone`, `make build`, `./bin/sluice version` | untested | not verified: `make build` not run. Target exists (Makefile:50-52) |
| 90 | bash | `make libduckdb`, `make build-static` | untested | not verified: 20 to 40 minute build |
| 101 | bash | `docker build -t sluice:dev .` | untested | not verified: no Docker |
| 111 | bash | Seed SQLite and `docker compose up` | broken | `data/` is not in the repo, so `sqlite3 data/shop.db` cannot create the file. Compose part not verified |

## docs/getting-started/quickstart.md

Prose words now: 347. Suggested after rewrite: 0 (merge into getting-started/index.md).

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5 | What the example holds | duplicate | Owner: getting-started/index.md (same list at lines 7-12) |
| 2 | 9 | `docker-compose.yaml` builds the image | mismatch | Doc: builds. Code: pulls the published image (examples/hello-sluice/docker-compose.yaml:10) |
| 3 | 10-17 | Files in the example and their kinds | implemented (examples/hello-sluice/policies.d/datasource-shop.yaml:3) | Matches `git ls-files examples/hello-sluice`. README.md and .gitignore are left out |
| 4 | 18 | `data/` directory in the example | mismatch | Not in the repo. Only ignore rules exist (examples/hello-sluice/.gitignore:3-5) |
| 5 | 28 | Seed creates `customers`, `orders`, `admin_users` for tenants acme, widget, globex | implemented (examples/hello-sluice/seed.sql:9-46) | Run with python3 sqlite3 |
| 6 | 57 | All declarative objects live in `policies.d/`. `secret://env/...` resolves from the environment. Compose sets the values | implemented (internal/secrets/env.go:24) | cmd/sluice/bootstrap.go:132, examples/hello-sluice/docker-compose.yaml:17-22. Run with exported vars |
| 7 | 80 | Key splits on the last `.`. HMAC-SHA256 over `id:material` with the pepper, constant-time compare with `hashRef` | implemented (internal/identity/apikey.go:209) | HMAC: :220-223, compare: :155. Run: demo key accepted |
| 8 | 87 | Binding stamps subject `hello`, tenant `acme`, groups `analytics` | implemented (cmd/sluice/bootstrap.go:770-781) | Seen in the audit record. The line `groups: "groups"` in the YAML has no effect here, key-level groups win (:770-773) |
| 9 | 112 | Default-deny: `ACL_DENIED` with `no SqlAccessPolicy matched (default-deny)` for `admin_users` | implemented (internal/policy/conflict.go:150) | Run: HTTP 403, same code and message |
| 10 | 120 | Compose pulls `ghcr.io/bino-bi/sluice:0.1.0-rc.2`, mounts three paths, sets three secrets | implemented (examples/hello-sluice/docker-compose.yaml:10-26) | Not run, no Docker |
| 11 | 120 | Run `sluice serve` directly: export the three variables, change `/data/audit` and `/data/shop.db` | implemented (cmd/sluice/serve.go:131-132) | Run this way for the whole audit. Without the variables the server exits 1: `audit genesis: env: "SLUICE_AUDIT_GENESIS" not set` |
| 12 | 142 | Five customers seeded, two `acme` rows returned, `email` is null | implemented (internal/queryservice/execute.go:326-337) | Run: `[[1,null,"acme"],[2,null,"acme"]]` |
| 13 | 146 | Validate before you reload | fluff | With `policies.reload: true` (server.yaml:30) saving the file is the reload. A bad file is rejected and the old policies stay (run) |
| 14 | 157 | Both commands exit non-zero with a kind, name and field message. Same pipeline as hot reload | implemented (cmd/sluice/policy.go:55-76) | cmd/sluice/config.go:71-85. Run: exit 3. Limit: secret refs and datasource attach are not checked. `config validate` passes with the env unset, `serve` then fails |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 7 | text | Directory tree of the example | untested | not verified: not runnable. Two labels are wrong, see claims 2 and 4 |
| 23 | bash | `cd examples/hello-sluice` and seed with sqlite3 | broken | `data/` does not exist on a fresh checkout, SQLite cannot open `data/shop.db`. Needs `mkdir -p data` |
| 34 | yaml | `server.yaml`, abridged | untested | verified by hand: every key equals examples/hello-sluice/server.yaml, `config validate --strict` exit 0, full file ran the scratch server |
| 63 | yaml | `SubjectBinding` demo-apikeys | untested | verified by hand: equals binding-apikey.yaml without comments, `policy validate` OK, key accepted by the server. No test loads this file |
| 82 | bash | `sluice apikey hash` with output | untested | verified by hand: output identical. cmd/sluice/apikey_test.go:18 tests the command, not this value |
| 93 | yaml | `SqlAccessPolicy` allow-analytics | tested | Same content as allow-analytics.yaml, loaded by `newHelloSluiceService` (internal/queryservice/function_gate_duckdb_test.go:109) |
| 116 | bash | `docker compose up` | untested | not verified: no Docker |
| 124 | bash | First query with curl | untested | verified by hand against 127.0.0.1:18100 |
| 132 | json | Response of the first query | untested | verified by hand: same fields and rows, only `query_id` differs. Header was `X-Sluice-Applied-Policies: allow-analytics,filter-tenant,mask-email` |
| 148 | bash | `policy validate` and `config validate` with output | untested | verified by hand from the repo root: output identical, digest `5ec2ef1b5ff4`. From `examples/hello-sluice` (where line 24 left the reader) it prints `0 objects` and exit 0. The digest changes with any byte in the example files |

## docs/getting-started/first-query.md

Prose words now: 442. Suggested after rewrite: 0 (steps move into getting-started/index.md, the rest to reference/rest-api.md and policies/index.md).

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 7-23 | Whole "Anatomy of POST /v1/query" section | duplicate | Owner: reference/rest-api.md (lines 28-80 hold the same table). Rows 2 to 9 check the facts |
| 2 | 13 | `sql`: exactly one statement, writes rejected | implemented (internal/rewriter/rewriter.go:360) | Run: two statements give 400 `ERR_MULTIPLE_STATEMENTS` (pkg/errors/codes.go:18), `DELETE` gives 403 `ACL_REJECTED` |
| 3 | 14 | `params`: not supported, `$n` refused with `ERR_UNSUPPORTED_SYNTAX` | implemented (internal/queryservice/execute.go:155-162) | Run: 400. A `params` array without `$n` in the SQL is accepted and ignored. On rc.2 params were supported |
| 4 | 15 | `max_rows` is clamped to the server limit | implemented (internal/queryservice/execute.go:100-105) | Run: `max_rows: 1` gives 1 row, `truncated: true` and a `warning` object |
| 5 | 16 | `timeout_ms` is clamped to the server maximum | implemented (internal/queryservice/execute.go:94-99) | Read only |
| 6 | 17 | `format`: `json` (default) or `csv` | implemented (internal/transport/rest/query.go:91-97) | Run: both |
| 7 | 18 | `meta`: free-form key/values, stored as `client_meta`, size-capped | implemented (internal/queryservice/audit_helpers.go:43-52) | Run: seen in the audit record. Values must be strings (pkg/apitypes/dto.go:15). Caps: 16 keys, 64 and 256 bytes |
| 8 | 20 | Response fields `query_id` (ULID), `columns`, `rows`, `row_count`, `truncated` | implemented (pkg/apitypes/dto.go:29-38) | Run. `warning` is missing from the list |
| 9 | 20 | Two headers on every response | mismatch | `X-Sluice-Applied-Policies` only on 200 with applied policies (internal/transport/rest/query.go:65-68). `X-Query-Id` only when a query id exists (internal/transport/rest/errors.go:33-35). Run: 400 arrow and 401 have neither |
| 10 | 23 | `format: arrow` is refused with `ERR_UNSUPPORTED_SYNTAX`, `arrow output not yet supported` | implemented (internal/transport/rest/query.go:98-100) | Run: 400, same message |
| 11 | 50 | Each matching table becomes a filtered subquery with `$1`. `customers` subquery lists columns with `NULL AS email` | implemented (internal/queryservice/execute.go:281-287) | Exact strings confirmed with `sluice policy test`. Shape also in internal/rewriter/wrap_test.go:234 |
| 12 | 54 | Stock mask nulls `email`. `partial` keeps the first character | implemented (pkg/mask/partial.go:15) | Run |
| 13 | 75 | Hot reload picks the change up on save | implemented (internal/config/watch.go:216) | Run: log line `config watch: reload applied` about 1 s after the write. Default `policies.reload: true` (internal/config/server.go:318) |
| 14 | 98 | Header `X-Sluice-Applied-Policies: allow-analytics,filter-tenant,mask-email` | implemented (internal/transport/rest/query.go:120-126) | Run: identical |
| 15 | 102 | `policy explain` answers without sending a query | implemented (cmd/sluice/policy.go:89-236) | Run |
| 16 | 123 | `--json` gives machine-readable output | implemented (cmd/sluice/policy.go:232) | Run |
| 17 | 127 | Default is `Enforce`. `Audit` keeps the policy loaded and matched and suspends its effect | implemented (internal/policy/conflict.go:42-44) | Default: internal/policy/snapshot.go:403-405. Run. The match is not reported anywhere, see "Not implemented". `DryRun` also exists (pkg/apitypes/common.go:86) |
| 18 | 127 | Useful for trialing a new mask | not implemented | Looked for the shadow match in the audit record, response headers and `policy explain`: none. `Decision.Shadow` (internal/policy/decision.go:35) has no reader outside internal/policy |
| 19 | 134 | In `Audit` mode emails come back in clear and `mask-email` leaves the header and `policies_applied` | implemented (internal/policy/conflict.go:42-44) | Run: both seen |
| 20 | 134 | The query is still audited in every mode | implemented (internal/queryservice/execute.go:386-395) | Run. True for requests that reach the pipeline, see index.md claim 6 |
| 21 | 140 | Delete the line or set `Enforce` to turn the mask back on | implemented (internal/policy/snapshot.go:403-405) | Run: mask applied again once the file no longer held the line |
| 22 | 144 | MCP has `explain_access`, `execute_sql`, `whoami` and six more tools | implemented (internal/transport/mcp/tools.go:25-81) | Read only, MCP not run |
| 23 | 148-150 | "Next" links | fluff | Navigation |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 29 | yaml | `RowFilterPolicy` filter-tenant | tested | Same content as filter-tenant.yaml, loaded by `newHelloSluiceService` (internal/queryservice/function_gate_duckdb_test.go:109) |
| 56 | yaml | `ColumnMaskPolicy` with `partial` | untested | verified by hand: `policy validate` OK, live reload, result as shown |
| 77 | bash | Same query again | untested | verified by hand against 127.0.0.1:18100 |
| 85 | json | Response with partial mask | untested | verified by hand: `"a**************"` and `"b************"`, only `query_id` differs |
| 104 | bash | `sluice policy explain` | untested | verified by hand. Path is relative to the repo root, the edit at line 54 is relative to `examples/hello-sluice` |
| 110 | text | Output of `policy explain` | untested | verified by hand: byte-identical after the partial mask edit. With the stock policy the last line says `null (via mask-email)` |
| 129 | yaml | Fragment `enforcementMode: Audit` | untested | verified by hand: added under `spec`, reload applied, mask off |
| 136 | bash | `sluice audit verify` | untested | verified by hand on the scratch audit dir: `chain OK (1 file(s), 23 record(s), last_hash=...)`, exit 0. Under Docker the files belong to uid 65532, not verified |
