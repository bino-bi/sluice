<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# CEL conditions

Sluice embeds [CEL](https://cel.dev) in three places, each with a different contract:

1. **`spec.conditions`** — available on every enforcement kind. Each named expression must
   type-check as `bool` at load time. At evaluation time, *all* conditions must be true for the
   policy to apply; if any expression errors at runtime, the **whole request is denied** — an
   erroring condition is never treated as merely false.
2. **`QueryRejectPolicy` rule expressions** — `spec.reject.rules[].expression`. A rule with an
   empty expression always fires; otherwise the rule fires when the expression is true. An
   evaluation error denies the request.
3. **`RowFilterPolicy` `spec.filter.expression`** — a restricted subset that is *lowered to SQL*
   at compile time, never evaluated in-process (see below).

## Variables

Conditions and reject rules see three variables:

| `subject.` | `request.` | `query.` |
| ---------- | ---------- | -------- |
| `id` | `remote_ip` | `has_select_star`, `is_aggregate` |
| `issuer` | `user_agent` | `has_cte`, `is_recursive_cte`, `has_union` |
| `email` | `headers` (map) | `has_limit`, `limit`, `has_order_by` |
| `groups` (list) | | `has_where`, `where_columns`, `group_by_columns` |
| `auth_method` | | `joins` (count), `catalogs`, `tables` |
| `claims` (map) | | |

`query.tables` lists the referenced tables as `catalog.schema.table` keys; `subject.auth_method`
is one of `jwt`, `api_key`, `admin_token`, `none`.

!!! note "There is no `now` variable"
    Policy decisions may be served from the rewrite cache, which is keyed on the policy snapshot,
    the SQL, and the identity. A time-dependent expression would make cached decisions wrong, so
    wall-clock time is deliberately not exposed.

!!! tip "Guard claim lookups"
    Accessing a missing map key is a CEL runtime error, and a runtime error denies the request.
    That is fail-closed but rarely what you want to express — write
    `'tier' in subject.claims && subject.claims.tier == 'gold'` to make the absent-claim case an
    ordinary `false`.

## The row-filter subset

`filter.expression` compiles into the same parameterised predicate tree as `filter.predicate` —
CEL never renders SQL text, and values bind as `$N` parameters. It sees `subject`, `request`, and
`row` (whose field selections become column references). Only these forms are accepted:

| Allowed | Example |
| ------- | ------- |
| `row.<col>` compared with `== != < <= > >=` to a literal or a `subject.*`/`request.*` reference | `row.tenant_id == subject.claims.tenant_id` |
| `&&`, `\|\|`, `!` | `row.region == 'eu' && !(row.tier == 'internal')` |
| `row.<col> in [literals]` | `row.status in ['open', 'pending']` |
| `row.<col>.startsWith/endsWith/contains("literal")` | `row.sku.startsWith('EU-')` |

Rejected at load time: arithmetic, function calls, macros (`has`, `exists`, …), any `query.*`
reference, and dynamic arguments to the string matchers. `sluice policy validate` catches all of
these before the policy can go live.

## Copy-paste conditions

```yaml
# fragment — conditions blocks to attach to any policy spec
conditions:
  # Only apply this policy to members of a group.
  - name: analysts-only
    expression: "'analysts' in subject.groups"

  # Claim-tier gate, safe against a missing claim.
  - name: gold-tier
    expression: "'tier' in subject.claims && subject.claims.tier == 'gold'"

  # Only apply to requests from JWT-authenticated subjects.
  - name: jwt-only
    expression: "subject.auth_method == 'jwt'"

  # Region gate driven by a claim list, guarded the same way.
  - name: eu-region
    expression: "'allowed_regions' in subject.claims && 'eu' in subject.claims.allowed_regions"
```

A complete `QueryRejectPolicy` using shape and request facts:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: QueryRejectPolicy
metadata:
  name: query-guardrails
  priority: 100
spec:
  match:
    any:
      - resources: { tables: ["*"] }
  reject:
    rules:
      - name: no-select-star-with-joins
        expression: "query.has_select_star && query.joins > 0"
        message: "SELECT * is not allowed in joins; name the columns you need"
        code: ACL_REJECTED
      - name: internal-network-only
        expression: "!request.remote_ip.startsWith('10.')"
        message: "queries must come from the internal network"
```

!!! tip "Prefer `ipRanges` for network checks"
    The `internal-network-only` rule above is only a string-prefix check. For real CIDR matching,
    use the [`subjects.ipRanges` selector field](matching.md#subject-fields) instead and keep CEL
    for what selectors cannot express.

## The same tenant filter, twice

Both forms compile to the identical parameterised SQL predicate
(`... WHERE tenant_id = $1`) — pick whichever reads better to your team:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: RowFilterPolicy
metadata: { name: tenant-isolation-predicate, priority: 80 }
spec:
  match:
    any:
      - resources: { tables: ["orders"] }
  combine: restrictive
  filter:
    predicate:
      column: tenant_id
      op: Equals
      value: "{{ subject.claims.tenant_id }}"
---
apiVersion: sluice.bino.bi/v1alpha1
kind: RowFilterPolicy
metadata: { name: tenant-isolation-cel, priority: 80 }
spec:
  match:
    any:
      - resources: { tables: ["invoices"] }
  combine: restrictive
  filter:
    expression: "row.tenant_id == subject.claims.tenant_id"
```

A `filter` must set exactly one of `predicate` or `expression`. If the subject has no
`tenant_id` claim, the template cannot render and the request is denied — missing identity never
degrades into an unfiltered query.
