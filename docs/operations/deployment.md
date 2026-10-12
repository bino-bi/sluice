<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Deployment

Run Sluice as a container or as a system service.

## Example

Goal: start Sluice with TLS on the admin listener and check that it is ready.

```yaml
--8<-- "examples/deployment/server.yaml:admin"
```

Start it:

```bash
--8<-- "examples/deployment/serve.sh:start"
```

Check readiness:

```bash
--8<-- "examples/deployment/steps/02-ready.sh"
```

```json
--8<-- "examples/deployment/expected/02-ready.txt"
```

Call the admin API:

```bash
--8<-- "examples/deployment/steps/04-admin-healthz.sh"
```

```json
--8<-- "examples/deployment/expected/04-admin-healthz.txt"
```

What happened: the data source is healthy. The admin API answered over TLS, with the token.

## Ports and health endpoints

| Port | Key | Serves |
|---|---|---|
| `8080` | `rest.listen` | Queries, health endpoints, approval links. |
| `9091` | `admin.listen` | The [Admin API](../reference/admin-api.md) and `/metrics`, with `admin.enabled: true`. |
| None | `mcp.listen` | [MCP](../reference/mcp.md) over HTTP. Set it for `mcp.transport: streamable_http`. |

Keep the admin port off the public network.

| Endpoint | Listener | Answer |
|---|---|---|
| `GET /v1/health` | REST | `200` while the process runs. |
| `GET /v1/ready` | REST | `200`, or `503` with status `degraded` while a data source is unhealthy. |
| `GET /v1/version` | REST | The build. |
| `GET /admin/healthz` | Admin | The same status, plus `config_version`. Needs the token. |

The REST endpoints need no login.

```bash
--8<-- "examples/deployment/steps/01-health.sh"
```

```json
--8<-- "examples/deployment/expected/01-health.txt"
```

```bash
--8<-- "examples/deployment/steps/03-version.sh"
```

```json
--8<-- "examples/deployment/expected/03-version.txt"
```

## TLS

`rest.tls` and `admin.tls` take the same keys. The lowest version is TLS 1.2.

| Key | Meaning |
|---|---|
| `certFile`, `keyFile` | Certificate and key as PEM files. Both are required. Sluice does not start if it cannot read them. |
| `clientCA` | A CA file for mutual TLS. Clients must then present a certificate that this CA signed. |

Without a `tls` block, a listener serves plain HTTP and logs a warning. The example creates a test certificate:

```bash
--8<-- "examples/deployment/serve.sh:cert"
```

## Container

| Tag | Content |
|---|---|
| `ghcr.io/bino-bi/sluice:<version>` | `linux/amd64` and `linux/arm64`. Use this tag. |
| `<version>-amd64`, `<version>-arm64` | One architecture. |
| `latest` | Published only by a stable release, never by a pre-release. |

- The tag has no leading `v`: release `v0.1.0-rc.2` is tag `0.1.0-rc.2`.
- The images are signed with cosign. See [Install](../getting-started/install.md).
- The image has no shell and runs as user `65532` in `/var/lib/sluice`. This user must read the mounted files and write the audit log. For the compose file below, run `serve.sh` once, then `sudo chown -R 65532:65532 data` in `examples/deployment`.
- The image declares `8080`, `8081` and `9090`. Publish the ports that you configured.
- `docker build -t sluice:dev .` builds your own image. `SLUICE_IMAGE=sluice:dev` makes the example compose files use it.
- `--build-arg LIBDUCKDB_JOBS=8` raises the 4 jobs of its DuckDB compile.

```yaml
--8<-- "examples/deployment/manual/docker-compose.yaml:2"
```

## systemd

```ini
--8<-- "examples/deployment/manual/sluice.service:2"
```

## Signals and exit status

| Signal | Effect |
|---|---|
| `SIGTERM`, `SIGINT` | Sluice stops. REST requests get 10 seconds to finish, admin requests 5. |
| `SIGHUP` | Sluice [reloads](hot-reload.md) the policy directory. |

Exit status 1 means a start error, 2 a listener error.

## DuckDB extensions

Most data source types need a DuckDB extension. Some builds download it to `~/.duckdb/extensions` at first use.

| Build | Included | Downloads for |
|---|---|---|
| Container images | All extensions | Nothing |
| `linux_amd64` release, `make build-static` | `httpfs`, `excel`, `sqlite_scanner` | `postgres`, `mysql`, `motherduck` |
| `linux_arm64` and `darwin` releases, `make build` | Nothing | Every type except `duckdb_file` |

`make build-static` first runs `make libduckdb`, a slow compile. `scripts/fetch-duckdb-extensions.sh` fills the directory ahead of time.

## See also

- [Server config & secrets](server-config.md)
- [Hardening](../security/hardening.md)
