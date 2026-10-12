<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Access control

A `SqlAccessPolicy` allows or denies access to tables. Sluice denies a query unless an allow covers every table that it reads.

## Example

Analysts may read `orders`.

```yaml
--8<-- "examples/access-control/policies.d/allow-analysts.yaml:2"
```

A query on the allowed table:

```bash
--8<-- "examples/access-control/steps/01-allowed-table.sh"
```

```json
--8<-- "examples/access-control/expected/01-allowed-table.txt"
```

A query on another table:

```bash
--8<-- "examples/access-control/steps/02-other-table.sh"
```

```json
--8<-- "examples/access-control/expected/02-other-table.txt"
```

The allow names only `orders`. No policy allows `customers`, so Sluice denies the second query.

## Fields

| Field | Default | Meaning |
|---|---|---|
| `effect` | required | `allow` or `deny`, in lower case. |
| `match` | required | Subjects and tables. See [Matching](matching.md). |
| `message` | `access denied` | For a deny: the error message. |
| `errorCode` | `ACL_DENIED` | For a deny: the error code. |

`exclude`, `conditions` and `enforcementMode` work as on [every policy](index.md).

`resources.actions` limits a clause to statement types: `SELECT`, `INSERT`, `UPDATE` or `DELETE`, in upper case. `EXPLAIN SELECT` counts as `SELECT`.

## More examples

### Every table needs an allow

```bash
--8<-- "examples/access-control/steps/03-join.sh"
```

```json
--8<-- "examples/access-control/expected/03-join.txt"
```

### A deny wins over an allow

Admins may read every table, but nobody reads `salaries`.

```yaml
--8<-- "examples/access-control/policies.d/allow-admins.yaml:2"
```

```yaml
--8<-- "examples/access-control/policies.d/deny-salaries.yaml:2"
```

```bash
--8<-- "examples/access-control/steps/04-admin-salaries.sh"
```

```json
--8<-- "examples/access-control/expected/04-admin-salaries.txt"
```

A deny on one table denies the whole query. If several denies match, the one with the highest priority gives the message.

```yaml
--8<-- "examples/access-control/tests/access.yaml:deny"
```

### Actions

```yaml
--8<-- "examples/access-control/tests/access.yaml:actions"
```

### Deny, then reject, then approval

Sluice checks denies first. A denied query gets no reject rule, row filter, column mask or approval. Reject rules come next. An approval can hold only an allowed query.

```yaml
--8<-- "examples/access-control/tests/access.yaml:order"
```

## Limits

- Sluice runs only read statements. A write gets `ACL_REJECTED`, also when an allow lists its action. See [Request lifecycle](../architecture/request-lifecycle.md).
- A statement without a table, such as `SELECT 1`, needs an allow clause that names no table.
- With another [policy engine](../architecture/policy-engines.md), a query without a matching allow can still run.

## See also

- [Matching](matching.md)
- [Rejects and rewrites](guardrails.md)
- [Approvals](approvals.md)
- [Error codes](../reference/error-codes.md)
