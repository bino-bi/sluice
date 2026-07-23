<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Server config & secrets

`sluice serve --config sluice.yaml` reads one YAML file for the process
runtime; policies live in their own directory. This page shows every
top-level block with its default values — the generated
[configuration reference](../reference/configuration.md) is the
authoritative field table.

## Annotated sluice.yaml

```yaml
# fragment — server config (not a policy manifest); values shown are defaults
# except the audit.file and identity blocks, which are illustrative
rest:
  listen: ":8080"
  maxBodyBytes: 1048576        # request body cap in bytes
  requestTimeout: 30s
  # tls: {certFile: ..., keyFile: ...}   # unset = plaintext

mcp:
  enabled: false
  transport: stdio             # stdio | streamable_http
  listen: ""                   # required for streamable_http
  sessionIdleMax: 30m

admin:
  enabled: false               # true in production: reload, explain, /metrics
  listen: ":9091"
  token: ""                    # empty = dev mode; inject via SLUICE_ADMIN__TOKEN

duckdb:
  memoryLimit: 4GB
  threads: 0                   # 0 = DuckDB default
  tempDir: ""
  maxOpen: 4
  maxIdle: 2
  connMaxIdle: 5m

datasources:
  directory: ./datasources.d   # examples keep DataSource manifests in policies.d
  failFast: true               # abort boot when a source fails to attach
  reload: false                # setting true is rejected at load; DataSource changes need a restart

policies:
  directory: ./policies.d      # empty directory = valid = deny everything
  reload: true                 # fsnotify hot reload
  engine: yaml                 # yaml | opa | composite
  composite: {members: [yaml]} # may add "opa", "rebac"
  opa: {moduleDir: "", query: data.sluice.main}
  rebac: {cacheTtl: 10s, cacheSize: 10000}

audit:
  failClosed: true             # refuse to serve unaudited queries
  sqlSampleBytes: 2048         # sql_sample cap per record; 0 disables
  file:                        # unset = $TMPDIR/sluice-audit + loud warning
    path: /var/lib/sluice/audit
    rotateDaily: true
    rotateSizeMB: 64
    genesis: secret://env/SLUICE_AUDIT_GENESIS   # chain anchor seed

logging:
  level: info                  # debug | info | warn | error
  format: json                 # json | text

identity:
  apiKeyPepper: secret://env/SLUICE_APIKEY_PEPPER   # HMAC pepper for API keys

limits:
  maxRows: 100000
  maxRowsCeiling: 100000
  maxSqlBytes: 1048576
  queryTimeout: 30s
  maxQueryTimeout: 30s
  maxConcurrent: 100
  disableCrossCatalog: false   # true rejects multi-catalog queries (ACL_REJECTED)
  globalRps: 500               # token bucket over all /v1/query traffic, before auth; 0 disables
  globalBurst: 1000
  perIpRps: 0                  # per-remote-IP bucket on /v1/query; keep 0 behind a load balancer
  perIpBurst: 0
  perIpMaxBuckets: 10000       # LRU bound on the per-IP bucket map
  defaultSubjectRps: 0         # fallback per-subject rate when a binding has no rateLimit; 0 disables
  defaultSubjectBurst: 0

cache:
  rewrite: {enabled: false, size: 4096, ttl: 60s}

approval:
  publicBaseUrl: ""            # required once an ApprovalPolicy loads
  webhooks: []                 # [{url, headersRef, timeout}]
  syncWait: 20s
  requestTtl: 15m
  grantTtl: 5m
  maxPending: 1000
  sqlSampleBytes: 2048         # SQL text cap in webhook payloads; 0 disables

budget:
  enabled: false
  stateDir: ./state
  flushInterval: 5s
  failClosed: true
  retentionDays: 35
```

## Precedence

Later layers win: **defaults → config file → `SLUICE_*` environment
variables → command-line flags**. A missing config file is fine — defaults
plus env and flags still produce a working server. A malformed file is
always fatal.

## Environment overrides

Fields are overridden with the `SLUICE_` prefix; dots in the YAML path
become double underscores:

```bash
export SLUICE_REST__LISTEN=":8443"        # rest.listen
export SLUICE_LOGGING__LEVEL="debug"      # logging.level
export SLUICE_LIMITS__MAXROWS="50000"     # limits.maxRows
```

Overrides work for fields that have built-in defaults or appear in your
config file. Fields without built-in defaults — notably `admin.token`,
`mcp.listen`, `identity.apiKeyPepper`, and the `audit.file.*` block — must
be present in `sluice.yaml` (an empty value is fine) for their `SLUICE_*`
override to take effect; otherwise the variable is silently ignored. For
example, keep `admin: {token: ""}` in the file so `SLUICE_ADMIN__TOKEN`
binds.

## secret:// references

Fields that carry sensitive material take a `secret://` URI instead of a
literal. Two providers work today:

- `secret://env/VAR_NAME` — process environment variable (case-sensitive).
- `secret://file//etc/sluice/secrets/pepper` — file contents. Note the
  double slash: it keeps the path absolute. Group- or world-writable secret
  files are refused; world-readable files log a warning. Trailing
  whitespace is trimmed for string secrets.

A `#fragment` suffix parses as a subfield selector for JSON-encoded
secrets (e.g. `secret://vault/secret/data/pii#value`); the `env` and
`file` providers return the whole value.

!!! warning "Not yet implemented — rejected at parse"
    `secret://vault/...`, `secret://aws-sm/...`, and `secret://gcp-sm/...`
    are **rejected** wherever a reference is parsed — `sluice config
    validate` exits 3 for server-config fields, and boot/reload fails for
    manifest refs. Only `env` and `file` resolve in this build.

Where references are accepted:

| Field | Object |
| ----- | ------ |
| `identity.apiKeyPepper` | server config |
| `audit.file.genesis` | server config (audit chain anchor) |
| `approval.webhooks[].headersRef` | server config (JSON map of header → value) |
| `spec.jwt.hmacSecretRef` | `SubjectBinding` |
| `spec.apiKeys[].hashRef` | `SubjectBinding` |
| `spec.credentialsRef` | `DataSource` (postgres, mysql, s3_parquet) |
| `spec.backend.tokenRef` | `RelationshipPolicy`; also `spec.tokenRef` on the motherduck `DataSource` |
| `spec.mask.args.saltRef` / `keyRef` / `seed` | `ColumnMaskPolicy` (`hash` hmac_sha256, `fpe`, `jitter`) |

Resolved values are cached for 10 minutes. When API-key auth is configured
(`identity.apiKeyPepper` set), the cache is also invalidated on every
policy reload, so rotating a secret plus touching the policy directory
takes effect immediately; otherwise rotated secrets are picked up within
the 10-minute TTL.

## Pre-deploy gate

```bash
sluice config validate ./policies.d --config sluice.yaml --strict
```

Validates the server file and every manifest in the directory; `--strict`
additionally rejects unknown YAML fields (catches typos). Exit codes:
`0` success, `1` I/O error, `3` validation failure. Run it in CI together
with [`sluice policy test`](../policies/testing.md).
