# Audit: Operations

Audited at commit `ad57e1d` (code is `6f7b99f`). Binary: `/tmp/sluice-bin/sluice`.
"hand" in a note means I ran it on a local server (hello-sluice example, SQLite and a local DuckDB file, ports 18180 to 18184). "read" means I only read the code.
Not run: Docker, cosign, Prometheus, systemd, Postgres, MySQL, S3, MotherDuck, an OTLP collector.

## Summary

### Not implemented

- index.md:16, server-config.md:42, data-sources.md:11: `datasources.directory`. The key is declared with a default (`internal/config/server.go:87,313`) and never read. `serve` loads DataSource manifests only from the policy directory (`cmd/sluice/bootstrap.go:130-136`).
- data-sources.md:167: `spec.schemas` and `spec.tables` do not restrict anything. They are copied into `Filters` (`internal/datasource/registry.go:188-191`) and no driver reads them. hand: `schemas: ["nonexistent"]`, `tables: ["customers"]`, and `shop.main.orders` still returned rows.
- data-sources.md:169, 220: `spec.healthCheck` (`query`, `interval`). Declared in `pkg/apitypes/datasource.go:51,86-89`, read nowhere. hand: a broken `query` left the source healthy. The health loop is fixed at 30 s (`internal/datasource/registry.go:81,120-122`). "`-1` disables it" is a Go option used by tests, there is no config key.
- data-sources.md:168: `spec.attachMode`. Forwarded (`registry.go:262-264`), read by no driver, not validated (hand: `attachMode: bogus` passes `config validate`). The page says `readwrite` is for a future release.
- observability.md:62-66: the `sluice_policy_*` and `sluice_schema_*` metrics are never exported by `sluice serve`. `policy.RegisterMetrics` (`internal/policy/metrics.go:31`) and `schema.RegisterMetrics` (`internal/schema/metrics.go:107`) have no caller outside tests. hand: after one allowed and one denied query, `/metrics` held only `sluice_audit_enqueued_total`, `sluice_audit_queue_depth`, `sluice_build_info`. The suggested alert on `sluice_policy_denials_total` can never fire.
- observability.md:21-23: "sensitive material is wrapped in `telemetry.Redacted`". The type exists (`internal/telemetry/slog.go:97-105`) and has no call site outside tests. I found no code that backs the guarantee for "logs, panics, metrics, and audit records".
- server-config.md:144-146: the `#fragment` selector on `secret://` refs. Parsed (`internal/secrets/urls.go:66`), used by no provider.
- server-config.md:148-152: `secret://vault`, `aws-sm`, `gcp-sm`. Rejected at parse (`internal/secrets/urls.go:27-31,57-60`). Only `env` and `file` are registered (`internal/secrets/resolver.go:59-62`).
- observability.md:142-143: DB driver spans (otelsql). Roadmap text, no such import.
- observability.md:147-151: `postgres` and `otlp` audit sinks. Rejected in `pkg/apitypes/decoder.go:524-533`.
- hot-reload.md:106-108: a Prometheus counter for reloads. The page says it does not exist. It does not.
- deployment.md:15: Helm chart. The page says there is none. No chart and no Kubernetes manifests are in the repo.

### Docs and code disagree

- data-sources.md:200-203: doc says DDL, `ATTACH`, `LOAD`, `INSTALL` return `ERR_UNSUPPORTED_SYNTAX` and `SET`, `SHOW`, `PRAGMA` are on the allowlist. hand: `CREATE TABLE t (a int)`, `DROP TABLE`, `ALTER TABLE`, `ATTACH`, `LOAD`, `INSTALL`, `SET`, `SHOW TABLES`, `PRAGMA version`, `SELECT 1` all return `ACL_DENIED` 403, also with an allow on `*.*.*`. Policy runs before the statement gate (`internal/queryservice/execute.go:194-222`, `internal/policy/conflict.go:145-153`, gate in `internal/rewriter/rewriter.go:353-368`). Only DDL that reads an allowed table (`CREATE TABLE x AS SELECT ...`) returns `ERR_UNSUPPORTED_SYNTAX`. `INSERT`/`UPDATE`/`DELETE` (`ACL_REJECTED`) and `COPY` (`ERR_UNSUPPORTED_SYNTAX`) match the doc.
- data-sources.md:215: doc says `datasource check` exits `2` when any source fails. Code: invalid or unknown-type manifests exit `1` (`cmd/sluice/datasource.go:76-85`), exit `2` only when no DataSource manifest is found (`datasource.go:87-90`). hand.
- data-sources.md:220-223: doc says a source that dies flips unhealthy within one interval, `/v1/ready` turns 503 and queries return `ERR_DATASOURCE_UNAVAILABLE`. hand (SQLite file removed under a running server): after 56 s `/v1/ready` was still 200 healthy and queries returned `ERR_INTERNAL` 500. The probe only reads `information_schema.schemata` (`internal/datasource/drivers/sqlitefile/driver.go:184`, same in every driver). `ERR_DATASOURCE_UNAVAILABLE` needs a failed attach or pool error (`internal/queryservice/execute.go:584-585`). Postgres and MySQL not tested.
- data-sources.md:192: doc lists released `windows` binaries. Code: `goos: [linux, darwin]` only (`.goreleaser.yaml:36-43`).
- hot-reload.md:78-79: doc says queries that match a reload-added ApprovalPolicy without `approval.publicBaseUrl` "pend until expiry". hand: they fail at once with `ERR_INTERNAL` 500, "approval required but no approval broker configured" (`internal/queryservice/execute.go:251-259`). The error log line is real (`cmd/sluice/bootstrap.go:494-497`) and repeats the wrong text.
- hot-reload.md:34: doc says the watcher covers the tree recursively. Code: only directories that exist at start (`internal/config/watch.go:97,240-256`). hand: a YAML file in a sub-directory made after start gave no reload.
- hot-reload.md:30-32: doc says no component sees a half-applied state on any reload path. Code: only the YAML policy compile runs before publish (`cmd/sluice/bootstrap.go:402-405`). OPA module errors and JWT binding conflicts fail per component after publish and the rest still switches (`bootstrap.go:451-480`). read.
- server-config.md:161: doc `spec.jwt.hmacSecretRef`. Code `spec.hmacSecretRef` (`pkg/apitypes/subjectbinding.go:18`).
- server-config.md:168-172: doc says the secret cache is cleared on reload only when `identity.apiKeyPepper` is set. Code clears it on every reload (`cmd/sluice/bootstrap.go:446-450`).
- server-config.md:151-152: doc says boot or reload fails for manifest refs to vault and friends. hand: `config validate --strict` exits 0 for a manifest with `hashRef: secret://vault/...`, and the reload is applied with a WARN that skips the key (`cmd/sluice/bootstrap.go:754-760`).
- server-config.md:6-7: doc says the page shows every top-level block. The `tracing` block is missing (`internal/config/server.go:28,329-332`). Missing keys: `mcp.tokenRef`, `mcp.apiKeyRef`, `mcp.allowAnonymous`, `admin.tls`, `rest.tls.clientCA`, `rest.tls.clientAuth`, `audit.syslog`, `audit.s3`, `approval.persist`, `approval.stateDir`.
- server-config.md:109-110: doc lists "command-line flags" as the top layer. Code passes no flag set to the loader (`cmd/sluice/bootstrap.go:93`). The only flag that overrides a key is `--policies-dir` (`bootstrap.go:130-133`).
- server-config.md:180-181: doc reads as if `--strict` also checks the server file. hand: a misspelled key and an unknown block in `sluice.yaml` pass with `--strict`, exit 0. Strict mode covers manifests only (`cmd/sluice/config.go:67-70`).
- observability.md:96-97: the jq example prints `.error_code` for `deny` records. A deny record never has `error_code`, only `message` (`internal/queryservice/audit_helpers.go:100-105`). hand: 20 of 20 deny records had none.
- observability.md:70-71: doc names drop reasons for s3 and syslog only. Code also counts `queue_full` and `context` for every sink (`internal/audit/dispatcher.go:224-236`).

### Broken code blocks

- None fails as written, with one fragment: data-sources.md:105 has no `apiVersion`, `kind`, `metadata`. `config validate` exits 3 on it. With the header added it passes.
- deployment.md:88: the fragment is "adapted from examples/hello-sluice" but drops `SLUICE_APIKEY_PEPPER` and `SLUICE_APIKEY_HELLO_HASH`. hand: with the hello-sluice `server.yaml` and no pepper variable, `serve` exits 1 ("resolve apiKeyPepper: env: not set"). The named volume `sluice-audit` is not declared, and that example writes audit to `/data/audit`, not to the mounted path.
- observability.md:93: see the `.error_code` point above. jq is not installed here, so the block itself was not run.

### Proposed pages for this section

1. `operations/index.md`: keep, short. One table of what an operator touches (two listeners, policy directory, audit directory, state directory) and links. Remove the `datasources.directory` row. Move the checklist to `security/hardening.md` or cut it: each item repeats another page.
2. `operations/deployment.md`: image tags, one compose example included from a tested example file, systemd unit, file layout, probes, ports, shutdown signals. Owns the health endpoint table and the "which build needs network for DuckDB extensions" table.
3. `operations/server-config.md`: how config loads (file, `SLUICE_*`, `--policies-dir`), the "key must exist in the file" rule, `secret://env` and `secret://file`, `config validate`. Replace the annotated YAML with a link to `reference/configuration.md`, or include a tested example file.
4. `operations/data-sources.md`: one example per type, what is always true (read-only attach, restart to change), `datasource check`, readiness. Remove `schemas`, `tables`, `attachMode`, `healthCheck`, the build reasons for extensions and the rename note. The statement rules belong to one security or reference page.
5. `operations/hot-reload.md`: triggers, what a reload applies, what needs a restart, how to confirm. Keep.
6. `operations/observability.md`: logs, `/metrics` with a link to `reference/metrics.md`, tracing. Remove the health table (owner: deployment) and the audit tail part (owners: `security/audit.md`, `reference/admin-api.md`).

No merge or removal of a page proposed.

### Features the code has that the section does not document

- Shutdown: `SIGINT` and `SIGTERM` drain REST up to 10 s and admin up to 5 s (`cmd/sluice/serve.go:41-52`, `internal/transport/rest/server.go:204-206`, `internal/transport/admin/server.go:190-192`). `serve` exits 1 on a boot error, 2 on a listener error (`serve.go:46,125`). hand: both signals stop the server.
- `admin.tls` and mutual TLS with `clientCA` on both listeners, TLS 1.2 minimum (`internal/config/server.go:71,280-285`, `internal/transport/tlsutil/tlsutil.go:30,43`). Only `security/hardening.md` covers it.
- `GET /admin/healthz` returns `config_digest` and `config_version`, a direct way to confirm a reload (`internal/transport/admin/healthz.go:20-42`). hand.
- `datasources.failFast: false`: a source that cannot be built is skipped with a WARN and is missing from `/v1/ready` (`internal/datasource/registry.go:145-154`). hand.
- `sluice datasource check [name]` and `--json` (`cmd/sluice/datasource.go:63-71,150`).
- More secret ref fields: `mcp.tokenRef`, `mcp.apiKeyRef`, `audit.s3.credentialsRef` (`internal/config/validate.go:67-94`).
- The loader skips hidden directories and `tests` and `testdata` sub-directories. Every other `.yaml` or `.yml` file in the policy directory must be a manifest (`internal/config/policies.go:171-185`). hand: a `server.yaml` inside the directory fails the load.
- `config validate` and `datasource check` do not run driver checks. hand: `s3` without `region` and `motherduck` without `tokenRef` pass both and fail `serve` (`internal/datasource/drivers/s3/driver.go:72-75`, `drivers/motherduck/driver.go:50-53`).
- The `s3` `credentialsRef` value must be JSON `{"key_id","secret","session_token"}` (`drivers/s3/driver.go:58-62,276-281`).
- A wrong `--config` path is accepted without a message: defaults apply (`internal/config/server.go:392-401`). hand: `config validate --config /nope.yaml` prints "server config OK".

## docs/operations/index.md

Prose words now: 380. Suggested after rewrite: 150.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-7 | Intro: two listeners, one manifest directory, audit directory, local state | fluff | Summary of the table below. |
| 2 | 13 | Data listener `:8080` (`rest.listen`): `POST /v1/query`, health, approval links | implemented (internal/config/server.go:292) | Routes in `internal/transport/rest/routes.go:22-47`. hand. Approval routes exist only when the broker is built. |
| 3 | 14 | Admin listener `:9091`, off by default, bearer token, `/admin/*` and `GET /metrics` | implemented (internal/transport/admin/routes.go:18-26) | Defaults `server.go:301-304`, auth `admin/auth.go:16-33`. hand: 401 without token. |
| 4 | 15 | Policy directory `./policies.d` holds policies, SubjectBinding, DataSource. Hot-reloaded | implemented (cmd/sluice/bootstrap.go:130-136) | Default `server.go:317`. DataSource changes are not applied on reload, see hot-reload row 21. |
| 5 | 16 | `datasources.directory` (`./datasources.d`) as a separate directory | not implemented | Key never read. Looked for any use of `DataSources.Directory` in `cmd/` and `internal/`. |
| 6 | 17 | Audit directory `audit.file.path`: hash-chained JSONL, created `0750`, unset means `$TMPDIR/sluice-audit` plus warning | implemented (cmd/sluice/auditwire.go:34-54) | Mode in `internal/audit/file_sink.go:121,147`. hand: fallback directory created 0750, WARN logged. |
| 7 | 18 | Budget state `./state` (`budget.stateDir`), SQLite, when `budget.enabled` | implemented (cmd/sluice/budgetwire.go:20-37) | File `budget.db`, directory created 0700. read. |
| 8 | 22-31 | Day-2 task list | duplicate | Navigation. Owners: the five linked pages. |
| 9 | 33-35 | Related chapters | fluff | Links only. |
| 10 | 39-41 | `audit.failClosed` default `true`, refuses a query it cannot audit | implemented (cmd/sluice/bootstrap.go:306) | Default `server.go:334`. read. Owner: `security/audit.md`. |
| 11 | 42-47 | Admin token: `token: ""` in file plus `SLUICE_ADMIN__TOKEN`. Empty token is dev mode with a warning | duplicate | Owner: server-config.md. Correct, see server-config row 20. |
| 12 | 48-50 | Default-deny checked with a `policy test` case | implemented (internal/policy/conflict.go:145-153) | Advice. hand: unmatched table returns `ACL_DENIED`. |
| 13 | 51-54 | Agent bindings set `rateLimit` and `budget`, server sets `budget.enabled: true` | implemented (pkg/apitypes/subjectbinding.go:22-23) | Advice. Owner: `policies/subjects.md`. read. |
| 14 | 55-57 | `sluice config validate <dir> --config sluice.yaml --strict` and `sluice policy test <dir>` in CI | implemented (cmd/sluice/config.go:28-104) | hand: exit 0. |
| 15 | 58-59 | Peppers, key hashes, credentials, genesis seed are `secret://` refs | implemented (internal/config/validate.go:40-84) | `admin.token` is a literal, not a ref. |
| 16 | 61-62 | Field defaults live in the configuration reference | duplicate | Pointer to `reference/configuration.md`. |

### Code blocks

None.

## docs/operations/deployment.md

Prose words now: 611. Suggested after rewrite: 350.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-9 | Single binary, cgo (pg_query, DuckDB), links glibc, `CGO_ENABLED=0` does not build | implemented (Makefile:50-52) | hand: `ldd` shows libc and libstdc++. `CGO_ENABLED=0 go build` fails with "build constraints exclude all Go files". |
| 2 | 11-14 | Current release `v0.1.0-rc.2`, API may change, pin a tag | fluff | Goes stale. Tag exists in git, `CHANGELOG.md:75`. |
| 3 | 14 | Pre-releases are never tagged `latest` | implemented (.goreleaser.yaml:122-124,167-172) | read. |
| 4 | 15 | There is no Helm chart | not implemented | No `Chart.yaml`, no Kubernetes manifests, no unit file in the repo. |
| 5 | 24-29 | Tags: `<version>` multi-arch, `-amd64`, `-arm64`, `latest` for stable only | implemented (.goreleaser.yaml:116-172) | read. Registry not checked. |
| 6 | 31-32 | Tag omits the leading `v` | implemented (.goreleaser.yaml:121) | Same in `examples/hello-sluice/docker-compose.yaml:10`. |
| 7 | 34 | Image manifests signed with keyless cosign | implemented (.goreleaser.yaml:196-204) | Workflow `.github/workflows/release.yaml:16,109-112`. Not run. |
| 8 | 42-45 | Released image: `distroless/cc-debian13:nonroot`, uid 65532, workdir `/var/lib/sluice`, extensions under `/home/nonroot/.duckdb/extensions`, no download at runtime | implemented (Dockerfile.release:16-28) | read. |
| 9 | 46-49 | `-amd64` compiles in `httpfs`, `excel`, `sqlite_scanner` and pre-seeds the rest. `-arm64` pre-seeds all six | implemented (.goreleaser.yaml:57-73,110-149) | Fetch step `release.yaml:94-96`, `Makefile:156-160`. read. |
| 10 | 51-53 | Image declares `EXPOSE 8080 8081 9090`. Publish 8080 and 9091 | implemented (Dockerfile.release:30) | Also `Dockerfile:116`. The list does not hold the default admin port 9091. |
| 11 | 57-67 | Repo `Dockerfile` stages, cache rule, "~30 min cold" | internal detail | True (`Dockerfile:21-51,54,96`). A user needs only "docker build works and takes long the first time". |
| 12 | 63-64 | Local build runtime is `distroless/cc-debian12:nonroot` | implemented (Dockerfile:96) | Differs from the released image (debian13), reason in `Dockerfile.release:10-15`. |
| 13 | 73-75 | DuckDB compile uses 4 jobs, raise with `--build-arg LIBDUCKDB_JOBS=N` | implemented (Dockerfile:27-31) | read. |
| 14 | 77-78 | Every example compose file takes `SLUICE_IMAGE` | implemented (examples/hello-sluice/docker-compose.yaml:10) | All 5 compose files use it. |
| 15 | 110-113 | `SLUICE_ADMIN__TOKEN` binds only when `admin.token` is in the file, else dev mode | duplicate | Owner: server-config.md. hand. "silently" is not exact: a WARN is logged (`internal/transport/admin/server.go:108-110`). |
| 16 | 140-143 | `ExecReload` sends `SIGHUP`, reload without dropping connections. Keep secrets in an env file | implemented (cmd/sluice/serve.go:63-78) | hand: `kill -HUP` gave "config watch: reload applied". |
| 17 | 147-153 | File layout and suggested modes | implemented (internal/secrets/file.go:51-53) | Advice. Code facts: writable secret files refused, audit directory 0750 (`internal/audit/file_sink.go:121`). The state directory is created 0700 (`cmd/sluice/budgetwire.go:27`). Paths are suggestions, defaults are relative. |
| 18 | 157-161 | Health endpoints: `/v1/health`, `/v1/ready` (503 lists sources), `/admin/healthz` (token) | duplicate | Repeated in observability.md:107-113 and `reference/rest-api.md:123-124`. Keep it here. Code `internal/transport/rest/health.go:35-71`, `internal/transport/admin/healthz.go:20-42`. hand. |
| 19 | 165-168 | Ports 8080 and 9091 and what they serve | duplicate | Owner: index.md:13-14. hand. |
| 20 | 169 | `mcp.listen` for streamable HTTP, unset by default, `mcp.enabled: false`, stdio | implemented (internal/config/server.go:296-300) | Transport names `internal/transport/mcp/server.go:26-27`. read. |
| 21 | 171-173 | Keep 9091 private (reload, policy introspection, audit tail). TLS on 8080 with `rest.tls` or a proxy | implemented (internal/transport/rest/server.go:144-163) | Admin routes `admin/routes.go:18-26`. TLS tests `internal/transport/rest/tls_test.go`. |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 19 | bash | `docker pull` and `docker run ... version` | untested | not verified: Docker not usable, registry not reached. `sluice version` itself runs. |
| 36 | bash | `cosign verify` of the image | untested | not verified: no cosign. Identity and issuer match the release footer (`.goreleaser.yaml:232-237`). |
| 69 | bash | `docker build -t sluice:dev .` | untested | not verified: Docker not usable. |
| 80 | bash | `SLUICE_IMAGE=sluice:dev docker compose up` | untested | not verified: Docker not usable. |
| 88 | yaml | Compose service, "adapted from hello-sluice" | untested | not verified: Docker not usable. Differs from the example file, see "Broken code blocks". |
| 117 | ini | systemd unit | untested | not verified: no unit file in the repo, systemd not used. `ExecStart` and `SIGHUP` reload match the CLI. |

## docs/operations/server-config.md

Prose words now: 410. Suggested after rewrite: 300.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-6 | `sluice serve --config sluice.yaml` reads one YAML file, policies live in their own directory | implemented (cmd/sluice/bootstrap.go:93) | Flags `cmd/sluice/serve.go:131-132`. hand. |
| 2 | 6-9 | Page shows every top-level block with defaults | mismatch | `tracing` block missing (`server.go:28,329-332`). Ten keys missing, see summary. |
| 3 | 16-20 | `rest`: `listen :8080`, `maxBodyBytes 1048576`, `requestTimeout 30s`, optional `tls` | duplicate | Owner: `reference/configuration.md`. Matches `server.go:291-295`. |
| 4 | 22-26 | `mcp`: `enabled false`, `transport stdio` or `streamable_http`, `listen`, `sessionIdleMax 30m` | duplicate | Matches `server.go:296-300`. "listen required" is not validated: an empty value makes Go bind `:80` (`internal/transport/mcp/server.go:188`). read. |
| 5 | 28-31 | `admin`: `enabled false`, `listen :9091`, `token ""` | duplicate | Matches `server.go:301-304`. |
| 6 | 33-39 | `duckdb`: `memoryLimit 4GB`, `threads 0`, `tempDir`, `maxOpen 4`, `maxIdle 2`, `connMaxIdle 5m` | duplicate | Matches `server.go:305-311`. `threads 0` skips the SET (`internal/executor/harden.go:71-73`). |
| 7 | 42 | `datasources.directory: ./datasources.d` | not implemented | Never read. |
| 8 | 43 | `datasources.failFast: true` aborts boot when a source fails to attach | implemented (internal/datasource/registry.go:145-148) | More exact: when the source cannot be built (missing file, bad URL). hand: `true` exits 1, `false` skips the source with a WARN. |
| 9 | 44 | `datasources.reload: true` is rejected at load | implemented (internal/config/validate.go:36-39) | hand: `config validate` exit 3. |
| 10 | 46-52 | `policies`: `directory`, `reload true`, `engine yaml` (or `opa`, `composite`), `composite.members`, `opa`, `rebac` | duplicate | Matches `server.go:316-323`, values in `cmd/sluice/policyengine.go:23-46,60-82`. An unknown `engine` passes `config validate` and fails `serve` (hand: validate exit 0). |
| 11 | 54-61 | `audit`: `failClosed true`, `sqlSampleBytes 2048`, `file.path`, `rotateDaily`, `rotateSizeMB`, `genesis` | duplicate | Matches `server.go:333-336,140-145`. `syslog` and `s3` blocks not shown. |
| 12 | 63-66 | `logging`: `level info`, `format json`, `maxErrorBytes 4096` | duplicate | Matches `server.go:324-328`. Unknown level means info, unknown format means json, no error (`bootstrap.go:827-838`, `internal/telemetry/slog.go:28-33`). hand. |
| 13 | 68-69 | `identity.apiKeyPepper` | duplicate | `server.go:196-198`, used at `bootstrap.go:645-646`. |
| 14 | 71-85 | `limits`: 14 keys with defaults and comments | duplicate | Matches `server.go:337-347`. Uses: `bootstrap.go:263-267,310-320,354-362`. hand: `disableCrossCatalog` gives `ACL_REJECTED`, `maxRows` truncates. |
| 15 | 87-88 | `cache.rewrite`: `enabled false`, `size 4096`, `ttl 60s` | duplicate | Matches `server.go:348-354`. |
| 16 | 90-97 | `approval`: `publicBaseUrl`, `webhooks`, `syncWait 20s`, `requestTtl 15m`, `grantTtl 5m`, `maxPending 1000`, `sqlSampleBytes 2048` | duplicate | Matches `server.go:355-363`. `persist` and `stateDir` not shown. |
| 17 | 99-104 | `budget`: `enabled false`, `stateDir ./state`, `flushInterval 5s`, `failClosed true`, `retentionDays 35` | duplicate | Matches `server.go:364-370`. |
| 18 | 109-110 | Precedence: defaults, file, `SLUICE_*`, command-line flags | mismatch | Env over file over defaults is right (hand). Flags: only `--policies-dir` (`bootstrap.go:93,130-133`). |
| 19 | 110-112 | Missing file is fine, malformed file is fatal | implemented (internal/config/server.go:392-401) | Tests `TestLoadServer_MissingFile_NotAnError`, `TestLoadServer_RejectsMalformedYAML`. hand: malformed gives exit 1. |
| 20 | 116-131 | `SLUICE_` prefix, `.` becomes `__`. Keys without a default (`admin.token`, `mcp.listen`, `identity.apiKeyPepper`, `audit.file.*`) must be in the file or the variable is ignored | implemented (internal/config/server.go:388-390) | No default for these in `server.go:419-496`. hand: ignored without the key, bound with an empty key, for `admin.token`, `identity.apiKeyPepper`, `audit.file.path`. Same rule for `duckdb.tempDir`, `datasources.reload`, TLS blocks, `audit.syslog`, `audit.s3`. |
| 21 | 135-142 | Providers `env` and `file`. Double slash for absolute path, writable files refused, world-readable warns, trailing whitespace trimmed | implemented (internal/secrets/resolver.go:59-62) | `internal/secrets/env.go`, `file.go:28-58`, `resolver.go:103-109`. Tests in `internal/secrets/resolver_test.go`. read. |
| 22 | 144-146 | `#fragment` selects a subfield of a JSON secret | not implemented | Parsed, never used. |
| 23 | 148-150 | `secret://vault`, `aws-sm`, `gcp-sm` | not implemented | Rejected at parse. hand: `config validate` exit 3 for all three in server config fields. |
| 24 | 151-152 | Boot or reload fails for manifest refs to these providers | mismatch | hand: validate exit 0, reload applied with a WARN for `hashRef`. See summary. |
| 25 | 158-160 | Refs in `identity.apiKeyPepper`, `audit.file.genesis`, `approval.webhooks[].headersRef` | implemented (internal/config/validate.go:40-84) | Resolved at `bootstrap.go:594-597,645-646`, `cmd/sluice/approval.go:85-99`. Not listed: `mcp.tokenRef`, `mcp.apiKeyRef`, `audit.s3.credentialsRef`. |
| 26 | 161 | `spec.jwt.hmacSecretRef` on SubjectBinding | mismatch | Code: `spec.hmacSecretRef` (`pkg/apitypes/subjectbinding.go:18`). |
| 27 | 162-165 | `spec.apiKeys[].hashRef`, DataSource `credentialsRef`, `headersRef`, `tokenRef`, RelationshipPolicy `spec.backend.tokenRef` | implemented (pkg/apitypes/datasource.go:21,35,44) | `subjectbinding.go:55`, `policy_relationship.go:30`. read. |
| 28 | 166 | Mask args `saltRef`, `keyRef`, `seed` | implemented (pkg/apitypes/policy_columnmask.go:66,78,81) | `seed` is also read by the `fake` mask (`pkg/mask/fake.go:60-66`). Owner: policies section. |
| 29 | 168 | Resolved values are cached for 10 minutes | implemented (internal/secrets/resolver.go:43-47) | Test `TestCache_TTLExpiry`. read. |
| 30 | 168-172 | Cache cleared on reload only when API-key auth is configured | mismatch | Cleared on every reload (`bootstrap.go:446-450`). |
| 31 | 180-182 | `config validate` checks server file and manifests. Exit 0, 1, 3 | implemented (cmd/sluice/config.go:28-104) | Tests in `cmd/sluice/config_test.go`. hand. It also compiles policies. |
| 32 | 180-181 | `--strict` rejects unknown YAML fields | mismatch | Only in manifests (hand: exit 3 for a misspelled manifest field). Unknown keys in `sluice.yaml` pass (hand: exit 0). |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 13 | yaml | Annotated `sluice.yaml` with defaults | untested | verified by hand: `config validate <dir> --config <block> --strict` exits 0. That does not prove a key has an effect: `datasources.directory` has none. No `tracing` block. |
| 119 | bash | Three `SLUICE_*` exports | tested | `TestLoadServer_EnvOverride` (`internal/config/server_test.go:101`) covers `SLUICE_REST__LISTEN` and `SLUICE_LIMITS__MAXROWS`. `SLUICE_LOGGING__LEVEL` verified by hand. |
| 176 | bash | `sluice config validate ./policies.d --config sluice.yaml --strict` | untested | verified by hand: exit 0. Tests use `--config` but never `--strict`. |

## docs/operations/data-sources.md

Prose words now: 698. Suggested after rewrite: 400.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-8 | One manifest is one DuckDB catalog named after `metadata.name`. Tables are `catalog.schema.table` | implemented (internal/datasource/registry.go:343-349) | hand: `shop.main.customers`. |
| 2 | 10-11 | Examples keep manifests in `policies.d` | implemented (internal/config/policies.go:125-127) | hand. |
| 3 | 11-12 | Dedicated `datasources.directory` | not implemented | Never read. |
| 4 | 12-14 | Changing a DataSource needs a restart | duplicate | Owner: hot-reload.md. hand. |
| 5 | 18-31 | `postgres`: `connection` URL, `credentialsRef`, `readonly`, `schemas` | implemented (internal/datasource/drivers/postgres/driver.go:53-107) | read. Not attached: no Postgres. No Go test attaches Postgres, `drivers/postgres/driver_test.go` covers URL parsing only. Not in the doc: user and database are required in the URL, default port 5432, default `sslmode` `prefer`. |
| 6 | 33-46 | `mysql`: `mysql://` or `mariadb://` | implemented (internal/datasource/drivers/mysql/driver.go:50-108) | read. No Go test attaches MySQL. Default port 3306. |
| 7 | 48-60 | `sqlite`: `path` | implemented (internal/datasource/drivers/sqlitefile/driver.go:42-68) | hand. The file must exist at boot (`driver.go:56`). Test `TestGateSelectOneThroughSQLiteAttach`. |
| 8 | 62-73 | `duckdb_file`: `path` | implemented (internal/datasource/drivers/duckdbfile/driver.go:37-61) | hand with a local `.duckdb` file. Attach is used by `internal/queryservice/function_gate_duckdb_test.go:57-60`. |
| 9 | 77-84 | `s3`: one read-only view per `allowedPaths` entry, format by extension, `format:` for all paths, no globs for xlsx | implemented (internal/datasource/drivers/s3/driver.go:129-163) | `drivers/common/format.go:18-34`. Tests `TestResolveViews*`, `TestS3DriverAllFormatsThroughHardenedExecutor` (local HTTP server, not S3). Not run against S3. |
| 10 | 86-103 | `s3` fields and view names (`countries_csv`, `facts___parquet`, `plan_xlsx`) | implemented (internal/datasource/drivers/s3/driver.go:67-127) | Names from `drivers/common/s3path.go:103-119`. read. `region` is required only by the driver. `credentialsRef` must be JSON. `spec.prefix` is accepted and unused (`driver.go:76,118`). |
| 11 | 116-118 | `s3_parquet` was renamed, old manifests fail validation with a hint | implemented (pkg/apitypes/decoder.go:472-476) | hand: exit 3 with the hint. History, the error text is enough. |
| 12 | 122-126 | `http`: one view per URL, the list is the allow-list, no globs, same format rules, query strings are fine | implemented (internal/datasource/drivers/httpfile/driver.go:93-139) | Test `TestHTTPDriverAllFormatsAndHeadersThroughHardenedExecutor`. Not run by hand. |
| 13 | 143-146 | `headersRef` is a JSON map, one HTTP secret per origin, scoped to it | implemented (internal/datasource/drivers/httpfile/driver.go:214-250) | Test `TestHeaderSecretRendering`. read. |
| 14 | 148-160 | `motherduck`: `database`, `tokenRef` | implemented (internal/datasource/drivers/motherduck/driver.go:37-61) | read. No Go test attaches MotherDuck. `tokenRef` is required only by the driver. |
| 15 | 166 | `readonly` declares read-only. Attach is `READ_ONLY` in any case | implemented (internal/datasource/drivers/sqlitefile/driver.go:59,106) | The field has no effect in any driver (`postgres/driver.go:98`, `mysql/driver.go:98`, `duckdbfile/driver.go:52`, `motherduck/driver.go:56`, `s3/driver.go:116`). |
| 16 | 167 | `schemas` and `tables` restrict what the catalog exposes | not implemented | hand, see summary. |
| 17 | 168 | `attachMode`: `readonly` or `readwrite` (future) | not implemented | No driver reads it. |
| 18 | 169 | `healthCheck` `{query, interval}`, 30 s loop, `-1` disables | not implemented | hand, see summary. Only the fixed 30 s loop is real. |
| 19 | 171-172 | Credentials go into DuckDB secrets and never show in logs or audit | implemented (internal/datasource/drivers/postgres/driver.go:148-161) | `CREATE SECRET` for postgres, mysql, s3, http. MotherDuck uses `SET motherduck_token` (`motherduck/driver.go:108`). "never in logs" is read only, not proven. |
| 20 | 176-179 | Static builds (`make build-static`, `Dockerfile`, linux/amd64 release) compile in `httpfs`, `excel`, `sqlite_scanner` | implemented (Makefile:60-65) | `Dockerfile:79-91`, `.goreleaser.yaml:62-73`. Test under a build tag: `internal/executor/static_extensions_test.go:35`. Not built here. |
| 21 | 179-182 | Why `motherduck`, `mysql_scanner`, `postgres_scanner` cannot be embedded | internal detail | `scripts/build-libduckdb.sh:13-27`. |
| 22 | 184-189 | Images pre-seed the loadable extensions, bare `LOAD`, signature check | duplicate | Owner: deployment.md:42-49. Code `drivers/common/extensions.go:23-44`. |
| 23 | 191-194 | Host builds install missing extensions on first attach, from the network unless cached in `~/.duckdb` | implemented (internal/datasource/drivers/common/extensions.go:34-42) | hand: my non-static binary loaded `sqlite_scanner` from `~/.duckdb/extensions/v1.5.5`. |
| 24 | 192 | Released `darwin/windows/linux-arm64` binaries | mismatch | No windows release (`.goreleaser.yaml:36-43`). |
| 25 | 194-196 | Pre-seed with `scripts/fetch-duckdb-extensions.sh <platform> <dir> <ext>...` | implemented (scripts/fetch-duckdb-extensions.sh:27-49) | Not run: needs network. |
| 26 | 200-205 | Statement allowlist and the error codes for writes, `COPY`, DDL, `ATTACH`, `LOAD`, `INSTALL` | mismatch | hand, see summary. This rule set needs one owner page outside Operations. |
| 27 | 209-213 | `sluice datasource check --dir`, alias `ds`, loads manifests, checks type and structure | implemented (cmd/sluice/datasource.go:18-26,73-111) | hand. |
| 28 | 215 | Exit `2` when any source fails | mismatch | Exit 1 for invalid manifests, 2 only for "no DataSource manifests found". hand. |
| 29 | 215-217 | `check` opens no connection. `serve` attaches and probes. `/v1/ready` shows per-source health | implemented (internal/transport/rest/health.go:45-71) | hand: `check` passes with a missing SQLite file. |
| 30 | 219-220 | Every source starts unhealthy, one sweep at boot, then every 30 s | implemented (internal/datasource/health.go:51-61) | `registry.go:81,209`. Tests `TestSetPoolTriggersImmediateSweep`, `TestHealthSweepThroughSQLiteAttach`. The interval is not `healthCheck.interval`. |
| 31 | 220-223 | A dead source turns unhealthy within one interval, 503, `ERR_DATASOURCE_UNAVAILABLE` | mismatch | hand with SQLite, see summary. |
| 32 | 225-229 | `sluice schema export --kind DataSource` | implemented (cmd/sluice/schema.go:45-93) | hand: JSON Schema printed, exit 0. |
| 33 | 233-235 | One `SELECT` can join across catalogs. `limits.disableCrossCatalog: true` forbids it | implemented (internal/queryservice/execute.go:131-140) | hand: SQLite joined with a DuckDB file. With the flag: `ACL_REJECTED` 403. Test `TestExecute_CrossCatalogGate`. Postgres with S3 not run. |
| 34 | 235-237 | Link to the cross-source joins cookbook | duplicate | Pointer. |

### Code blocks

All eight manifests: `config validate --strict` and `datasource check` were run by hand.

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 20 | yaml | `postgres` DataSource | untested | verified by hand: validate and check exit 0. Attach not verified: no Postgres, and no test attaches one. |
| 35 | yaml | `mysql` DataSource | untested | verified by hand: validate and check exit 0. Attach not verified: no MySQL, no test. |
| 50 | yaml | `sqlite` DataSource | untested | verified by hand: same content as `examples/hello-sluice/policies.d/datasource-shop.yaml`, served queries. |
| 64 | yaml | `duckdb_file` DataSource | untested | verified by hand: served queries from a local DuckDB file. |
| 86 | yaml | `s3` DataSource with three paths | untested | verified by hand: validate and check exit 0. Attach not verified: no S3. Format logic tested by `TestS3DriverAllFormatsThroughHardenedExecutor`. |
| 105 | yaml | `s3` spec fragment with `format: parquet` | untested | Fails `config validate` as written (no `apiVersion`, `kind`, `metadata`). With the header: exit 0 (hand). |
| 128 | yaml | `http` DataSource | untested | verified by hand: validate and check exit 0. Attach not run by hand. Driver tested by `TestHTTPDriverAllFormatsAndHeadersThroughHardenedExecutor`. |
| 150 | yaml | `motherduck` DataSource | untested | verified by hand: validate and check exit 0. Attach not verified: needs MotherDuck, no test. |
| 209 | bash | `sluice datasource check --dir ./policies.d` | untested | verified by hand: exit 0, `ds` alias works. No test for this command. |
| 227 | bash | `sluice schema export --kind DataSource > file` | untested | verified by hand: exit 0. No test for this command in `cmd/sluice`. |

## docs/operations/hot-reload.md

Prose words now: 710. Suggested after rewrite: 300.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-7 | Policy directory reloads without restart. Running queries keep their snapshot, new queries get the new one | implemented (internal/policy/engine.go:28,76) | Reload verified by hand. The running-query part is read only. |
| 2 | 13 | fsnotify watcher, on by default (`policies.reload: true`) | implemented (cmd/sluice/serve.go:58-60) | hand: edit applied in about 1 s. Test `TestWatcher_ReloadOnChange`. |
| 3 | 14 | `SIGHUP` | implemented (cmd/sluice/serve.go:63-78) | hand. Not listed in `sluice serve --help`. |
| 4 | 15 | `POST /admin/reload` | implemented (internal/transport/admin/reload.go:20-43) | hand. Tests `TestHandleReload_*`. Details owner: `reference/admin-api.md:82`. |
| 5 | 17-19 | `policies.reload` gates only the watcher. `SIGHUP` and the endpoint always work | implemented (cmd/sluice/bootstrap.go:389-409) | hand with `reload: false`. Test `TestBuildRuntime_ReloadDisabled_ManualReloadStillWired`. |
| 6 | 27-30 | Success returns `{"ok": true, "digest": "..."}`. Failure returns `ERR_CONFIG_INVALID` or `ERR_POLICY_INVALID`, snapshot untouched | implemented (internal/transport/admin/reload.go:28-43) | hand: both codes seen, HTTP 400. |
| 7 | 30-32 | A snapshot that fails compilation is rejected before publish on every path, no half-applied state | mismatch | True for YAML policy compile only (`bootstrap.go:402-405`). See summary. |
| 8 | 34 | Watcher covers the directory tree recursively | mismatch | Only directories present at start. hand. |
| 9 | 35-36 | Reacts to `.yaml` and `.yml` only, ignores dotfiles and swap files, 250 ms debounce | implemented (internal/config/watch.go:86-88,225-235) | hand. A dot-named `.yaml` file does not trigger a reload, but the loader still reads it on the next reload (hand: `.hidden.yaml` made the reload fail). |
| 10 | 37 | `SIGHUP` and the endpoint reload at once | implemented (internal/config/watch.go:124-126) | hand. |
| 11 | 41-45 | Each reload reads the whole directory. Any error rejects it and the old snapshot serves | implemented (internal/config/watch.go:193-221) | hand: bad file, old policies kept serving. Test `TestWatcher_ValidateRejectsPublish`. |
| 12 | 47-50 | The reject log line lists the errors of all manifests, cut at `logging.maxErrorBytes`. Use `config validate` for the full list | implemented (internal/config/watch.go:184-187) | hand: two file errors in one WARN line. Cut in `internal/telemetry/slog.go:69-79`. |
| 13 | 54-55 | Swap updates the compiled policy set, OPA modules are recompiled | implemented (cmd/sluice/bootstrap.go:451-456) | `internal/opaengine/engine.go:72-93`. read. |
| 14 | 56-57 | API-key bindings rebuilt, secret cache cleared first | implemented (cmd/sluice/bootstrap.go:446-465) | Only when `identity.apiKeyPepper` is set (`bootstrap.go:457`). Test `TestBuildAPIKeyBindings`. |
| 15 | 58-62 | JWT bindings and HMAC secrets swapped. Duplicate issuers are rejected, old set stays | implemented (cmd/sluice/bootstrap.go:466-480) | Test `TestApplyReload_JWTBindings`. read. |
| 16 | 63 | Rate-limit and budget specs from SubjectBinding | implemented (cmd/sluice/bootstrap.go:481-487) | read. |
| 17 | 64 | Rewrite cache purged | implemented (cmd/sluice/bootstrap.go:488-490) | read. |
| 18 | 65-66 | Schema cache emptied, each catalog loads again on the next lookup | implemented (cmd/sluice/bootstrap.go:498-500) | `internal/schema/cache.go:162-181`. read. |
| 19 | 66-73 | Schema cache details: miss reloads the catalog, no timer, stale column list on masked tables, rewrite cache TTL | internal detail | True (`Refresh` has no caller). One user line is enough: after a column change at the source, reload. |
| 20 | 75-78 | ApprovalPolicy reloads like other kinds. The broker exists when `approval.publicBaseUrl` is set | implemented (cmd/sluice/approval.go:36-45) | Test `TestBuildApprovalBroker_BaseURLOnly`. read. |
| 21 | 78-79 | Without a base URL, a reload-added ApprovalPolicy logs an error and matching queries pend until expiry | mismatch | Error logged, queries fail at once with `ERR_INTERNAL`. hand. |
| 22 | 83-87 | `sluice.yaml` is read once. Listeners, engine, limits, base URL, DuckDB pool, audit config need a restart | implemented (cmd/sluice/bootstrap.go:93) | read. |
| 23 | 87-89 | DataSource attachments are built at boot, restart after a change | implemented (cmd/sluice/bootstrap.go:154-159) | hand: a new DataSource file gives "reload applied" and no new catalog. No warning is logged. |
| 24 | 93-100 | Rollout: validate and test in CI, deploy files, reload, check with `policy explain` or `GET /admin/subjects/explain` | implemented (internal/transport/admin/routes.go:20) | hand: all three commands ran. |
| 25 | 104-105 | An applied reload logs `config watch: reload applied` at INFO with object count and digest | implemented (internal/config/watch.go:216-219) | hand. |
| 26 | 105-106 | `POST /admin/reload` returns the new digest | duplicate | Same as row 6. |
| 27 | 106-108 | No Prometheus counter for reloads | not implemented | Correct that none exists. Drop the sentence. |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 21 | bash | `curl -X POST .../admin/reload` with bearer token | untested | verified by hand on port 18181: `{"ok":true,"digest":"..."}`. The handler has unit tests, the command does not. |

## docs/operations/observability.md

Prose words now: 571. Suggested after rewrite: 300.

### Claims

| # | Line | Claim | Class | Evidence or note |
|---|---|---|---|---|
| 1 | 5-6 | Three signals: logs, metrics, audit trail | fluff | Intro. |
| 2 | 10-11 | Structured JSON logs via `slog` on stderr, stdout stays clean | implemented (internal/telemetry/slog.go:19-33) | hand: JSON lines, `format: text` gives text. |
| 3 | 13-19 | `logging.level`, `format`, `maxErrorBytes` | duplicate | Owner: `reference/configuration.md`. Also in server-config.md:63-66. |
| 4 | 21-23 | Secrets are wrapped in `telemetry.Redacted`, guarantee for logs, panics, metrics, audit | not implemented | Type exists, no call site outside tests. |
| 5 | 25-29 | Query failure cause is not redacted. `ERR_INTERNAL` logs at error, `ERR_QUERY_FAILED` at warn, with `query_id` and cause. The log can hold SQL and cell values | implemented (internal/telemetry/slog.go:85-95) | Warn line at `internal/queryservice/execute.go:345-351`. hand: both lines seen, the warn line held the cell value `'Alice'`. |
| 6 | 31-34 | `error` text is cut after `logging.maxErrorBytes` (4096), `0` turns it off | implemented (internal/telemetry/slog.go:69-79) | Tests `TestNewSlogHandler_CutsErrorText`, `TestBuildRuntime_LoggedErrorCut`. Negative value rejected (`internal/config/validate.go:33-35`, hand). |
| 7 | 38-42 | `GET /metrics` on the admin listener, same bearer token, needs `admin.enabled`, empty token means no auth | implemented (internal/transport/admin/routes.go:26) | hand: 401 without token, 404 on the data port. |
| 8 | 62-64 | Full catalog is in the metrics reference | duplicate | Owner: `reference/metrics.md`. |
| 9 | 62-63 | Catalog holds policy evaluations, denials, schema cache | not implemented | Declared, never registered in `serve`. hand. The reference page lists them too. |
| 10 | 65 | Alert on `sluice_audit_dropped_total > 0` | implemented (internal/audit/metrics.go:35) | read. The series appears only after the first drop. |
| 11 | 65-66 | Alert on rising `sluice_policy_denials_total` | not implemented | Never exported. |
| 12 | 66-67 | Alert on `sluice_audit_queue_depth` near capacity | implemented (internal/audit/metrics.go:34) | hand: metric present. Capacity is fixed at 10000 (`internal/audit/dispatcher.go:74-75`), the page gives no number. |
| 13 | 69-72 | Drop labels: s3 `buffer_full` and `write_error`, syslog `write_error` | mismatch | Also `queue_full` and `context` for every sink (`internal/audit/dispatcher.go:224-236`). The named ones are in `s3_sink.go:141,211`, `syslog_sink.go:151`. |
| 14 | 89-90 | Every query writes hash-chained JSONL records | duplicate | Owner: `security/audit.md`. hand: fields seen. |
| 15 | 90-91, 101 | Admin tails the current file as NDJSON, `n` default 100, max 1000 | implemented (internal/transport/admin/audit.go:17-50) | hand: `application/x-ndjson`, `n=0` gives 400, larger values are clamped. Also in `reference/admin-api.md:96`. |
| 16 | 101-103 | Schema, `sluice audit verify`, retention are in the audit chapter | duplicate | Pointer. The command exists (hand: `--help`). |
| 17 | 107-113 | Health endpoint table, five endpoints | duplicate | Owner: deployment.md:157-161. hand: all five answer. `/admin/healthz` also returns 503 "degraded" (`internal/transport/admin/healthz.go:33-38`). |
| 18 | 117-126 | `tracing` block: `enabled`, `endpoint`, `protocol` grpc or http, `insecure`, `sampleRatio` | implemented (internal/config/server.go:186-192) | Checks in `validate.go:22-32` (hand: exit 3 for missing endpoint, bad protocol, ratio 2). Export not verified: no collector. |
| 19 | 128-133 | OTLP export, W3C `traceparent`, server span per REST and MCP HTTP request, `query` span with five children | implemented (internal/telemetry/tracing.go:30-91) | Span names `internal/queryservice/execute.go:43,109,196,283,335,360`. Test `TestTracing_ServerSpanPerRequest`. read. |
| 20 | 135-141 | Span attributes: query id, origin, decision, fingerprint, catalogs, error code. No raw SQL. `query` span ends before row streaming | implemented (internal/queryservice/execute.go:43-46,556-573) | read. |
| 21 | 142-143 | otelsql spans are on the roadmap | not implemented | No such dependency. |
| 22 | 147-149 | `postgres` and `otlp` audit sinks are not implemented | not implemented | Rejected at decode. |
| 23 | 149-151 | `syslog` and `s3` forwarding is set in `audit.*` server config, not in AuditSink manifests | implemented (cmd/sluice/auditwire.go:56-102) | Manifests of these types are rejected (`pkg/apitypes/decoder.go:515-523`). read. Owner: `security/audit.md`. |

### Code blocks

| Line | Lang | What | Class | Note |
|---|---|---|---|---|
| 13 | yaml | `logging` block | untested | verified by hand: `config validate --config` exit 0. |
| 44 | bash | `curl` of `/metrics` with bearer token | untested | verified by hand on port 18181: Prometheus text. |
| 51 | yaml | Prometheus scrape config | untested | not verified: no Prometheus. The bearer token need matches the code. |
| 74 | yaml | Alert rule on `sluice_audit_dropped_total` | untested | not verified: no Prometheus. Metric and labels exist. |
| 93 | bash | Audit tail piped to `jq`, deny records | untested | not verified: no jq. Endpoint and the fields `timestamp`, `subject.id`, `sql_fingerprint` verified with Python. `.error_code` is never set on a deny record. |
| 119 | yaml | `tracing` block | untested | verified by hand: `config validate --config` exit 0. Export not run. |
