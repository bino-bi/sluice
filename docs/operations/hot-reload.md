<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Configuration reload

Sluice loads the policy directory again without a restart. A running query finishes with the old policies.

## Example

Goal: allow a second table while the server runs.

```yaml
--8<-- "examples/reload/server.yaml:policies"
```

No policy allows `customers` yet:

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

```bash
--8<-- "examples/reload/steps/03-allowed.sh"
```

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

One invalid file stops the whole reload, and the old policies stay active. Here a file has a wrong `effect`:

```bash
--8<-- "examples/reload/steps/06-reload-fails.sh"
```

```json
--8<-- "examples/reload/expected/06-reload-fails.txt"
```

After a file change, the log shows the cause. So does `sluice policy validate`:

```bash
--8<-- "examples/reload/steps/07-validate.sh"
```

```text
--8<-- "examples/reload/expected/07-validate.txt"
```

`ERR_CONFIG_INVALID` means Sluice cannot load a file: broken YAML, an unknown kind, a wrong field value. `ERR_POLICY_INVALID` means a policy or a Rego module does not compile. Here a policy has an unknown tag:

```bash
--8<-- "examples/reload/steps/12-reload-fails-compile.sh"
```

```json
--8<-- "examples/reload/expected/12-reload-fails-compile.txt"
```

## Confirm a reload

`GET /admin/healthz` counts the reloads in `config_version`. The log shows `config watch: reload applied`.

```bash
--8<-- "examples/reload/steps/04-healthz.sh"
```

```json
--8<-- "examples/reload/expected/04-healthz.txt"
```

## Limits

- Sluice watches the policy directory for `.yaml` and `.yml` files, and `policies.opa.moduleDir` for `.rego` files. Hidden files do not count.
- It misses directories created after the start.
- A duplicate JWT issuer, or a secret of a `SubjectBinding` that cannot be resolved, does not stop the reload. Sluice warns and keeps the old bindings of that kind.
- An `ApprovalPolicy` needs `approval.publicBaseUrl` from the start. Without it, matching queries fail with `ERR_INTERNAL`.

## See also

- [Testing policies](../policies/testing.md)
- [Data sources](data-sources.md)
- [Observability](observability.md)
