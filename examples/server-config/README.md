<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# server-config

A server config that takes the admin token from an environment variable and its secrets from `secret://env` and `secret://file` references.

## Run

```bash
examples/server-config/serve.sh
```

The script creates `data/shop.db` from `seed.sql` and the secret file `data/secrets/apikey-pepper`, then starts Sluice on `http://localhost:8080`. Run the steps from this folder, for example `bash steps/01-validate.sh`.

| Step | Shows |
|---|---|
| `steps/01-validate.sh` | `sluice config validate` accepts `server.yaml` and `policies.d`. |
| `steps/02-validate-bad.sh` | It lists every error in `server-bad.yaml` and ends with status 3. |
| `steps/03-admin-without-token.sh` | The admin API refuses a call without the token. |
| `steps/04-admin-with-token.sh` | The token from `SLUICE_ADMIN__TOKEN` works. |
| `steps/05-query.sh` | The API key works, so Sluice read the pepper file. |

`expected/` holds the real output of each step. See [the examples guide](../README.md) for the layout.
