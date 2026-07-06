<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Embedded OPA (Rego)

Sluice can evaluate access decisions with an embedded [Open Policy Agent](https://www.openpolicyagent.org)
engine instead of — or alongside — the YAML engine. Choose Rego when your decision logic outgrows
declarative selectors (cross-cutting conditions, computed decisions) or when your organization
already maintains policy as Rego. Stay with YAML for everything it expresses directly: it is
easier to review, and it is what [`sluice policy test`](testing.md) exercises. The middle ground
is `composite`, which merges both deny-overrides — YAML keeps the guardrails, Rego adds logic on
top.

## Server configuration

```yaml
# fragment of sluice.yaml — server configuration, not a policy
policies:
  directory: ./policies.d
  engine: composite          # yaml (default) | opa | composite
  composite:
    members: [yaml, opa]
  opa:
    moduleDir: ./policies.d/opa   # top-level *.rego files only, non-recursive
    query: data.sluice.main       # default
```

Every `*.rego` file directly under `moduleDir` is loaded (subdirectories are ignored). Modules
are recompiled on every [policy reload](../operations/hot-reload.md); a broken module set fails
the reload and the previously compiled modules stay active. With `engine: opa` and no modules
loaded, every query fails — fail-closed.

## The input document

Your query (default `data.sluice.main`) is evaluated with this `input`:

```json
{
  "subject": {
    "id": "alice",
    "issuer": "https://idp.example.com",
    "email": "alice@example.com",
    "groups": ["analysts"],
    "auth_method": "jwt",
    "claims": { "sub": "alice", "tenant": "acme" }
  },
  "action": "SELECT",
  "tables": [
    { "catalog": "shop", "schema": "main", "table": "orders", "key": "shop.main.orders" }
  ],
  "shape": {
    "has_select_star": false, "is_aggregate": false, "has_cte": false, "has_union": false,
    "has_limit": true, "limit": 100, "has_where": true, "joins": 0
  },
  "request": { "remote_ip": "10.0.0.7", "user_agent": "curl/8.6.0", "headers": {} }
}
```

## The output contract

The rule must produce a single document with only these fields — **unknown fields are rejected
and the query fails, fail-closed**. An undefined result (no rule fired) is also a fail-closed
deny.

| Field | Type | Meaning |
| ----- | ---- | ------- |
| `allow` | bool | `false` (or absent) denies |
| `abstain` | bool | No opinion — lets other composite members decide |
| `deny_reason` | `{message, code, policy}` | Client-visible denial details |
| `row_filters` | `[{table, combine, predicate}]` | Predicates to inject; `table` must be a `key` from the input set; `combine` defaults to `restrictive` |
| `column_masks` | `[{table, column, mask}]` | `mask` is a YAML-engine `{type, args}` mask spec |
| `rejections` | `[{rule, message, code}]` | Any entry flips an allow to a rejection |

Rego never renders SQL: predicates and masks are re-compiled through the exact same paths as
their [RowFilterPolicy](row-filters.md) and [ColumnMaskPolicy](column-masks.md) YAML
equivalents — including `{{ subject.* }}` templates rendered as bound parameters. An invalid
predicate or mask, or a `row_filters`/`column_masks` entry referencing a table outside the
input set, fails the query. Error codes are normalized: a `code` that is not a known Sluice
code (see the [error-code reference](../reference/error-codes.md)) falls back to `ACL_DENIED`
(deny) or `ACL_REJECTED` (rejection), so Rego cannot mint arbitrary client-facing codes.

## A complete module

Allows the `analysts` group on the `shop` catalog and injects a per-tenant row filter on every
referenced table:

```rego
package sluice

import rego.v1

# An undefined main is already a fail-closed deny; the explicit default
# keeps the contract visible.
default main := {"allow": false}

main := {
	"allow": true,
	"row_filters": [{
		"table": t.key,
		"combine": "restrictive",
		"predicate": {
			"column": "tenant_id",
			"op": "Equals",
			"value": "{{ subject.tenantId }}",
		},
	} |
		some t in input.tables
	],
} if {
	"analysts" in input.subject.groups
	every t in input.tables {
		t.catalog == "shop"
	}
}
```

The predicate uses the PascalCase operator names of the YAML dialect (`Equals`, `In`,
`Between`, …) because it re-enters that compiler verbatim.

## Testing Rego policies

`sluice policy test` drives the YAML engine only — its suites do not evaluate your Rego
modules regardless of `policies.engine`. Test Rego the OPA-native way, with `opa test` against
fixture `input` documents shaped like the one above, and verify the wired result end to end by
running real queries against a dev instance.

!!! note "Explain tooling"
    The CLI `sluice policy explain` inspects only the YAML engine's snapshot — it never
    evaluates the OPA modules. `GET /admin/subjects/explain` runs the server's configured
    engine, so with `engine: opa` or `composite` it does evaluate the Rego modules (OPA member
    results appear as `OpaModule` entries). At query time an applied OPA decision shows up as
    `opa` in the `X-Sluice-Applied-Policies` response header and as the pseudo-policy kind/name
    `OpaModule`/`opa` in audit records — not as individual rule names.
