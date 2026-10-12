<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# deployment

A server with TLS on the admin listener and an admin token from an environment variable.

## Run

```bash
examples/deployment/serve.sh
```

The script creates `data/shop.db` from `seed.sql` and a self-signed certificate in `data/tls/`. It needs `openssl`. Sluice listens on `http://localhost:8080`, the admin API on `https://localhost:9091`. Then run the steps from this folder, for example `bash steps/01-health.sh`.

| Step | Shows |
|---|---|
| `steps/01-health.sh` | The process answers. |
| `steps/02-ready.sh` | Every data source is healthy. |
| `steps/03-version.sh` | The build of the running server. |
| `steps/04-admin-healthz.sh` | The admin API answers over TLS, with the token. |

`expected/` holds the real output of each step. See [the examples guide](../README.md) for the layout.

## Docker and systemd

`manual/docker-compose.yaml` runs the released image with the files of this folder. Run `serve.sh` once before, so `data/` exists. `manual/sluice.service` is a systemd unit. `make docs-check` does not run these two files.
