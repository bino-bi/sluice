<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# MCP tools

AI agents call Sluice through MCP, the Model Context Protocol. A tool call passes the same policies as a REST query.

## Example

Open a session over HTTP and store its id:

```bash
--8<-- "examples/mcp-agent/steps/01-initialize.sh"
```

Call the tool `execute_sql`:

```bash
--8<-- "examples/mcp-agent/steps/05-query.sh"
```

```text
--8<-- "examples/mcp-agent/expected/05-query.txt"
```

The answer holds the rows as text and as `structuredContent`.

## Tools

| Tool | Input | Returns |
|---|---|---|
| `execute_sql` | `sql` (required), `row_limit` | `query_id`, `columns`, `rows`, `row_count`, `truncated` |
| `list_catalogs` | none | `catalogs` with `name`, `type`, `healthy` |
| `list_tables` | `catalog` (required), `schema`, `limit`, `cursor` | `tables`, `next_cursor` |
| `list_accessible_tables` | `catalog`, `limit`, `cursor` | `tables`, `next_cursor` |
| `describe_table` | `table` (required) | `table`, `columns` with `name`, `type`, `nullable` |
| `explain_access` | `table` or `sql` | `subject`, `resource`, `decision`, `row_filters`, `column_masks`, `matched`, `rejected` |
| `whoami` | none | `anonymous`, `subject`, `issuer`, `email`, `groups`, `auth_method` |
| `check_approval` | `approval_id` (required) | `approval_id`, `state`, `expires_at` |
| `await_approval` | `approval_id` (required), `timeout_seconds` | The same, after a wait of at most 55 seconds |

- `row_limit` works like `max_rows` in the [REST API](rest-api.md).
- `table` is a name of the form `catalog.schema.table`. Sluice does not add a missing catalog or schema. A policy that names them does not match a shorter name.
- `limit` is 500 by default and at most 1000. Pass `next_cursor` as `cursor` to get the next page. A `cursor` that is not valid gives `ERR_SYNTAX`.
- `explain_access` runs no query.
- The approval tools read the state of a held query. An unknown id gives `ERR_APPROVAL_EXPIRED`. Without `approval.publicBaseUrl` both tools give `ERR_INTERNAL`. See [Approvals](../policies/approvals.md).
- A failed call has `isError: true`. Its text starts with the [error code](error-codes.md) and can name the policy and the query id.

## Transports

| Transport | How to start | How the subject is set |
|---|---|---|
| stdio | `sluice mcp --config server.yaml`, see [CLI](cli.md) | Once at start, from `--api-key` or `--jwt`. |
| stdio in `sluice serve` | `mcp.enabled: true` | Once at start, from `mcp.apiKeyRef` or `mcp.tokenRef`. |
| Streamable HTTP | `mcp.transport` and `mcp.listen`, as below | Each request sends `X-Api-Key` or `Authorization: Bearer`. Without it the answer is 401. |

```yaml
--8<-- "examples/mcp-tools/server.yaml:mcp"
```

`sluice mcp --transport streamable_http` listens on `mcp.listen` too. Stdio without a credential does not start. With `--allow-anonymous` or `mcp.allowAnonymous`, a client without a credential runs as an anonymous subject. Default deny still applies.

## Which tables an agent sees

`list_tables` and `list_accessible_tables` show a table only when both are true:

- A policy allows the subject to read the table.
- Sluice has loaded the schema of its data source. `describe_table` on one table that the subject can read does that. A query does it only for a table with a column mask.

A fresh server lists nothing. A reload empties the list again. So call `describe_table` first:

```bash
--8<-- "examples/mcp-tools/steps/06-describe-table.sh"
```

Then `list_tables` shows every table that the subject can read:

```bash
--8<-- "examples/mcp-tools/steps/07-list-tables-again.sh"
```

```text
--8<-- "examples/mcp-tools/expected/07-list-tables-again.txt"
```

`describe_table` answers `ACL_DENIED` for a table that the subject cannot read. For other tables it returns every column, masked ones too. `list_catalogs` hides a catalog when the subject can read none of its loaded tables. A catalog without loaded tables stays visible.

## Audit records

`execute_sql` and `explain_access` write audit records with `origin: mcp`. The other tools write none.

## See also

- [MCP agents](../cookbook/mcp-agents.md)
- [CLI](cli.md) for the flags of `sluice mcp`
- [Audit log](../security/audit.md)
