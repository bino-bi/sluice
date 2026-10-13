<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# OPA / Rego

Sluice can decide with Rego modules. Open Policy Agent (OPA) runs inside Sluice: you need no OPA server.

## Example

Goal: analysts read the `shop` catalog, but only the rows of their tenant.

```yaml
--8<-- "examples/opa/server.yaml:policies"
```

With `engine: opa`, Sluice evaluates no YAML policy. To combine both, see [Policy engines](../architecture/policy-engines.md).

```rego
--8<-- "examples/opa/rego/main.rego:2"
```

An analyst of the tenant `acme` reads the orders.

```bash
--8<-- "examples/opa/steps/01-analyst.sh"
```

```json
--8<-- "examples/opa/expected/01-analyst.txt"
```

A guest sends the same query.

```bash
--8<-- "examples/opa/steps/02-guest.sh"
```

```json
--8<-- "examples/opa/expected/02-guest.txt"
```

**What happened:** Sluice evaluated `data.sluice.main` for each query. For the analyst, the module returned `allow` and a row filter. For the guest, the default applied.

## Fields

| `input` key | Content |
|---|---|
| `subject` | `id`, `issuer`, `email`, `groups`, `auth_method`, `claims` |
| `action` | Statement kind, for example `SELECT` |
| `tables[]` | `catalog`, `schema`, `table`, and `key` as `catalog.schema.table` |
| `shape` | `has_select_star`, `is_aggregate`, `has_cte`, `has_union`, `has_limit`, `limit`, `has_where`, `joins` |

| Result field | Type | Default | Meaning |
|---|---|---|---|
| `allow` | boolean | `false` | `true` allows. `false` denies. |
| `abstain` | boolean | `false` | `true`: the other composite members decide. |
| `deny_reason` | object | | `message`, `code`, `policy` for the error. |
| `row_filters[]` | list | | `table` (a `key` from the input), `predicate` as in a [row filter](row-filters.md), `combine`. |
| `column_masks[]` | list | | `table`, `column`, and `mask` as in a [column mask](column-masks.md). |
| `rejections[]` | list | | `rule`, `message`, `code`. One entry rejects the query. |

An undefined result denies the query. The header `X-Sluice-Applied-Policies` and the audit record show the name `opa`.

## Limits

- `policies.opa.moduleDir` is required. Sluice loads the `.rego` files at its top level and does not start without one.
- A changed `.rego` file needs `POST /admin/reload` or SIGHUP. The file watcher sees only YAML files.
- A reload with a broken module still answers `ok`. The old modules stay active.
- An unknown output field, a bad predicate or mask, or a table outside the input gives 500 `ERR_INTERNAL`.
- An unknown `code` becomes `ACL_DENIED` or `ACL_REJECTED`.

## See also

- [Policy engines](../architecture/policy-engines.md)
- [Testing policies](testing.md)
- [Admin API](../reference/admin-api.md)
