<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Deployment

Sluice compiles to a single binary, but not a fully static one: the
PostgreSQL parser (`pg_query`) and embedded DuckDB are cgo libraries, so
the binary links against glibc. Build on (or target) a glibc-based Linux
and keep `CGO_ENABLED=1`; plain `GOOS=linux CGO_ENABLED=0` cross-builds do
not work.

!!! warning "Not yet implemented"
    There are no published release binaries, container images, or Helm
    charts yet. Build from source (`make build`) or use the repository
    `Dockerfile` — the `hello-sluice` example wraps it in a compose file.

## Docker

The repository `Dockerfile` is a multi-stage build: a `libduckdb` stage
compiles DuckDB with the `httpfs`/`excel`/`sqlite_scanner` extensions
statically linked (cached until `scripts/DUCKDB_VERSION` changes, ~30 min
cold), an `extensions` stage pre-seeds the non-embeddable
`postgres_scanner`/`mysql_scanner`/`motherduck` loadable extensions,
`golang:1.25-bookworm` compiles the binary against the bundle, and the
runtime stage is `gcr.io/distroless/cc-debian12:nonroot` (glibc +
libstdc++, uid/gid 65532, no shell). The image needs no outbound access
to `extensions.duckdb.org` at runtime for any datasource type. Working
directory is `/var/lib/sluice`; pre-seeded extensions live under
`/home/nonroot/.duckdb/extensions`.

```bash
docker build -t sluice:dev .
```

The DuckDB compile stage caps parallelism at 4 jobs so it fits Docker
Desktop's default ~8 GB VM; raise it with `--build-arg LIBDUCKDB_JOBS=N`
when the daemon has more memory.

Released container images differ per arch only in how extensions are
delivered: the `-amd64` image compiles `httpfs`/`excel`/`sqlite_scanner`
into the binary and pre-seeds the rest; the `-arm64` image uses the
prebuilt DuckDB bindings and pre-seeds all six. Both attach every
datasource type offline.

The image declares `EXPOSE 8080 8081 9090`; `EXPOSE` is metadata only, so
publish the ports you actually configure — `8080` (data) and `9091`
(admin) with the defaults. The `hello-sluice` compose service shows the
full pattern:

```yaml
# fragment — docker-compose service, adapted from examples/hello-sluice
services:
  sluice:
    build: {context: ., dockerfile: Dockerfile}
    ports:
      - "8080:8080"   # REST data plane
      - "9091:9091"   # admin plane (reload, explain, /metrics)
    environment:
      SLUICE_ADMIN__TOKEN: "${SLUICE_ADMIN_TOKEN}"
      SLUICE_AUDIT_GENESIS: "${SLUICE_AUDIT_GENESIS}"   # secret://env/... target
    volumes:
      - ./server.yaml:/etc/sluice/server.yaml:ro
      - ./policies.d:/etc/sluice/policies.d:ro
      - sluice-audit:/var/lib/sluice/audit
    command: ["serve", "--config=/etc/sluice/server.yaml",
              "--policies-dir=/etc/sluice/policies.d"]
    healthcheck:   # distroless has no shell or curl — run the binary
      test: ["CMD", "/usr/local/bin/sluice", "version"]
      interval: 10s
```

The `SLUICE_ADMIN__TOKEN` override only binds when the mounted
`server.yaml` contains the `admin.token` key — keep `admin: {token: ""}`
in the file, or the admin plane silently runs token-less in dev mode (see
[Server config & secrets](server-config.md)).

## systemd

```ini
# fragment — /etc/systemd/system/sluice.service
[Unit]
Description=Sluice SQL gateway
After=network-online.target
Wants=network-online.target

[Service]
User=sluice
Group=sluice
WorkingDirectory=/var/lib/sluice
EnvironmentFile=/etc/sluice/sluice.env
ExecStart=/usr/local/bin/sluice serve --config /etc/sluice/server.yaml
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/sluice

[Install]
WantedBy=multi-user.target
```

`ExecReload` sends `SIGHUP`, which triggers a validate-then-swap policy
reload without dropping connections — see
[Configuration reload](hot-reload.md). Keep `SLUICE_ADMIN__TOKEN` and other
secret env vars in `/etc/sluice/sluice.env` (mode `0600`).

## File layout and permissions

| Path | Suggested mode | Purpose |
| ---- | -------------- | ------- |
| `/etc/sluice/server.yaml` | `0640 root:sluice` | Server config. No secret literals — use `secret://` refs. |
| `/etc/sluice/policies.d/` | dir `0750`, files `0640` | Policy, binding, and data source manifests. Deployed read-only. |
| `/etc/sluice/secrets/` | files `0600 sluice` | Targets of `secret://file//...` refs. Group/world-writable secret files are refused. |
| `/var/lib/sluice/audit/` | `0750 sluice:sluice` | Hash-chained JSONL audit files (Sluice creates the directory `0750`). |
| `/var/lib/sluice/state/` | `0750 sluice:sluice` | Budget SQLite state (`budget.stateDir`), when budgets are enabled. |

## Health endpoints for probes

| Endpoint | Listener | Use as |
| -------- | -------- | ------ |
| `GET /v1/health` | data (`:8080`) | Liveness — process is up. |
| `GET /v1/ready` | data (`:8080`) | Readiness — returns `503` and lists data sources while unhealthy. |
| `GET /admin/healthz` | admin (`:9091`) | Admin-plane health (requires the admin bearer token). |

## Ports

| Port | Config key | Purpose |
| ---- | ---------- | ------- |
| `8080` | `rest.listen` | Data plane: `POST /v1/query`, health, version, approval accept/reject links. |
| `9091` | `admin.listen` | Admin plane: `/admin/*` and `GET /metrics`. Only served when `admin.enabled: true`. |
| — | `mcp.listen` | Streamable-HTTP MCP transport; unset by default (`mcp.enabled: false`, stdio transport). |

Keep `:9091` off the public network — it carries reload, policy
introspection, and audit tail. Front `:8080` with TLS (`rest.tls`) or a
terminating proxy.
