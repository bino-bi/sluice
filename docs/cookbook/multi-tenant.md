<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Multi-tenant isolation

Two tenants share one database. One row filter limits each API key to the rows of its tenant.

## Example

Start the server with `examples/multi-tenant/serve.sh`. Each tenant has a [subject binding](../reference/glossary.md). For API keys, the `claims` of a binding are fixed values.

```yaml
--8<-- "examples/multi-tenant/policies.d/bindings-tenants.yaml:2"
```

The row filter compares `tenant_id` with the `tenantId` claim.

```yaml
--8<-- "examples/multi-tenant/policies.d/filter-tenant.yaml:2"
```

The acme key reads the customers.

```bash
--8<-- "examples/multi-tenant/steps/01-acme.sh"
```

```json
--8<-- "examples/multi-tenant/expected/01-acme.txt"
```

The beta key sends the same SQL.

```bash
--8<-- "examples/multi-tenant/steps/02-beta.sh"
```

```json
--8<-- "examples/multi-tenant/expected/02-beta.txt"
```

**What happened:** Sluice put the filter around the table and bound the claim value as parameter `$1`. The data also holds a tenant `gamma`. No binding has that claim, so no client reads those rows.

This suite case shows the rewritten SQL. `sluice policy test policies.d --tests tests` runs the suite without a server.

```yaml
--8<-- "examples/multi-tenant/tests/isolation.yaml:rewritten"
```

## More examples

A join filters both tables.

```bash
--8<-- "examples/multi-tenant/steps/04-acme-join.sh"
```

```json
--8<-- "examples/multi-tenant/expected/04-acme-join.txt"
```

A subquery is filtered too. The table holds five orders. The acme key counts two.

```bash
--8<-- "examples/multi-tenant/steps/05-acme-subquery.sh"
```

```json
--8<-- "examples/multi-tenant/expected/05-acme-subquery.txt"
```

A `WHERE` clause cannot widen the filter. The acme key asks for beta rows and gets none.

```bash
--8<-- "examples/multi-tenant/steps/03-acme-asks-for-beta.sh"
```

```json
--8<-- "examples/multi-tenant/expected/03-acme-asks-for-beta.txt"
```

## Limits

- A subject without the `tenantId` claim gets `ACL_DENIED` and no rows.

## See also

- [Row filters](../policies/row-filters.md)
- [Subjects](../policies/subjects.md): API keys and claims
- [Testing policies](../policies/testing.md)
