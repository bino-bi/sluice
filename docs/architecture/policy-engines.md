<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Policy engines

The engine decides on each query: allow, deny or reject, and which row filters and column masks apply. You select it with `policies.engine` in the server config.

| `policies.engine` | Decides with |
|---|---|
| `yaml` (default) | The YAML policies in the policy directory |
| `opa` | Rego modules, run by an embedded Open Policy Agent. See [OPA](../policies/opa.md) |
| `composite` | The engines in `policies.composite.members`, in order |

A member of `composite` is `yaml`, `opa` or `rebac`. The member `rebac` checks `RelationshipPolicy` objects against OpenFGA. See [ReBAC](../policies/rebac.md).

Sluice checks these names at start. `sluice config validate` does not check them.

## Example

Goal: a YAML policy allows two tables. A Rego module denies a query that reads both.

```yaml
--8<-- "examples/policy-engines/server.yaml:engine"
```

```yaml
--8<-- "examples/policy-engines/policies.d/allow-analytics.yaml:2"
```

```rego
--8<-- "examples/policy-engines/rego/main.rego:2"
```

Sluice reads the rule `main` of the package `sluice`. Change this with `policies.opa.query`.

A query on one table runs:

```bash
--8<-- "examples/policy-engines/steps/01-one-table.sh"
```

```json
--8<-- "examples/policy-engines/expected/01-one-table.txt"
```

A join of both tables is denied:

```bash
--8<-- "examples/policy-engines/steps/02-rego-denies.sh"
```

```json
--8<-- "examples/policy-engines/expected/02-rego-denies.txt"
```

What happened: for one table the module abstains (gives no opinion), so the YAML policy decides alone. For the join the module denies, and a deny from any member wins.

## What each engine can return

| Result | `yaml` | `opa` | `rebac` |
|---|---|---|---|
| Allow | yes | yes | no |
| Deny | yes | yes | yes |
| Reject | yes | yes | no |
| Row filters, column masks | yes | yes | no |
| Row limit, sample, timeout | yes | no | no |
| Approval | yes | no | no |
| Abstain (no opinion) | no allow matches | `abstain: true` | no check fails |

A Rego module that returns no result denies the query. An unknown field in its result fails the request with `ERR_POLICY_UNAVAILABLE`.

## How `composite` merges

1. An error in one member fails the request with `ERR_POLICY_UNAVAILABLE`. An OpenFGA server that does not answer within the policy's `backend.timeout` gives `ERR_TIMEOUT`.
2. A deny from any member wins.
3. Then a reject from any member wins.
4. One member must allow. An allow from Rego is enough, also for a table that no YAML policy allows. If all members abstain, Sluice denies the query.
5. On allow, the restrictions of all members apply. Row filters join with AND. For one column, the mask of the first member wins. The lowest row limit and timeout win, and the first sample. Approvals add up.

## Limits

- `rebac` works only as a member of `composite`.
- `opa` needs `policies.opa.moduleDir`. Without it the server does not start.
- Sluice does not watch `.rego` files. After an edit, send `SIGHUP` or call `POST /admin/reload`.
- The rewrite cache (`cache.rewrite.enabled`) works only with `yaml`.
- `sluice policy test` and `sluice policy explain` use the YAML engine only. `GET /admin/subjects/explain` and the MCP tool `explain_access` use the configured engine.

## See also

- [Policies](../policies/index.md)
- [Testing policies](../policies/testing.md)
- [Reload](../operations/hot-reload.md)
