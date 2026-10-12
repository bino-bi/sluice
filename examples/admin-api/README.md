<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# admin-api

Calls to the admin API with a bearer token, and their real responses.

## Run

```bash
examples/admin-api/serve.sh
```

The script creates `data/shop.db` from `seed.sql`. It starts Sluice on `http://localhost:8080` and the admin API on `http://localhost:9091`. Then run the steps in order from this folder.

| Step | Shows |
|---|---|
| `steps/01-policies.sh` | The loaded policies by policy kind. |
| `steps/02-datasources.sh` | The state of each data source. |
| `steps/03-explain.sh` | The policies that apply to one subject and one table. |
| `steps/04-reload.sh` | A reload of the policy directory. |
| `steps/05-audit-tail.sh` | The newest audit record. It is the record of step 03. |
| `steps/06-healthz.sh` | The health of the server and the digest of the loaded policies. |

`expected/` holds the real output of each step. See [the examples guide](../README.md) for the layout.
