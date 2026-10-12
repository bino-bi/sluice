<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Configuration reload

Sluice loads the policy directory again without a restart. A running query finishes with the old policies.

## Example

Goal: allow a second table while the server runs. No policy allows `customers` yet:

```bash
--8<-- "examples/reload/steps/01-denied.sh"
```

```json
--8<-- "examples/reload/expected/01-denied.txt"
```

Copy this file into the policy directory:

```yaml
--8<-- "examples/reload/changes/allow-customers.yaml:2"
```

```bash
--8<-- "examples/reload/steps/02-add-policy.sh:cmd"
```

The same query now returns rows:

```json
--8<-- "examples/reload/expected/03-allowed.txt"
```

What happened: Sluice saw the new file and reloaded the directory 250 ms later.

## Triggers

| Trigger | Use |
|---|---|
| A file changes | Automatic. `policies.reload: false` turns it off. |
| `SIGHUP` | `systemctl reload sluice` or `kill -HUP`. |
| `POST /admin/reload` | From a script. See [Admin API](../reference/admin-api.md). |

The last two always work and reload at once.

```bash
--8<-- "examples/reload/steps/10-reload.sh"
```

```json
--8<-- "examples/reload/expected/10-reload.txt"
```

## What a reload applies

- Every policy, also the OPA modules.
- Subject bindings: API keys, JWT issuers, HMAC secrets, rate limits and budgets.
- Secrets of policy objects. Sluice reads each reference again.
- Table columns. Reload after a column changes at a data source.

These need a restart: the server config and every `DataSource` change.

## An invalid file

One invalid file stops the whole reload, and the old policies stay active. `POST /admin/reload` then returns:

```json
--8<-- "examples/reload/expected/06-reload-fails.txt"
```

The other code is `ERR_POLICY_INVALID`. After a file change, the log names the file. So does `sluice policy validate`:

```text
--8<-- "examples/reload/expected/07-validate.txt"
```

## Confirm a reload

`GET /admin/healthz` counts the reloads in `config_version`. The log shows `config watch: reload applied`.

```json
--8<-- "examples/reload/expected/04-healthz.txt"
```

## Limits

- The watcher reacts only to `.yaml` and `.yml` files that are not hidden.
- The watcher misses folders created after the start.
- A reload resets every rate limit bucket.
- A duplicate JWT issuer or a broken OPA module does not stop the reload. Sluice warns and keeps the old state of that part.
- An `ApprovalPolicy` needs `approval.publicBaseUrl` from the start. Without it, matching queries fail with `ERR_INTERNAL`.

## See also

- [Testing policies](../policies/testing.md)
- [Data sources](data-sources.md)
- [Observability](observability.md)
