<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# reload

Sluice applies a new policy file without a restart, and keeps the old policies when a file is invalid.

## Run

```bash
examples/reload/serve.sh
```

The script creates `data/shop.db` from `seed.sql`, copies `policies.d` to `data/policies` and starts Sluice on `http://localhost:8080`. The server loads `data/policies`, so the steps can change files. Run the steps in order from this folder, for example `bash steps/01-denied.sh`.

| Step | Shows |
|---|---|
| `steps/01-denied.sh` | No policy allows `customers` yet. |
| `steps/02-add-policy.sh` | Copy `changes/allow-customers.yaml` into the policy directory. |
| `steps/03-allowed.sh` | The same query now returns rows. |
| `steps/04-healthz.sh` | `config_version` went from 1 to 2. |
| `steps/05-add-broken.sh` | Copy an invalid file into the policy directory. |
| `steps/06-reload-fails.sh` | `POST /admin/reload` reports the failed reload. |
| `steps/07-validate.sh` | `sluice policy validate` names the file and the error. |
| `steps/08-still-allowed.sh` | The old policies still answer. |
| `steps/09-remove-broken.sh` | Remove the invalid file. |
| `steps/10-reload.sh` | `POST /admin/reload` succeeds. |

`expected/` holds the real output of each step. See [the examples guide](../README.md) for the layout.
