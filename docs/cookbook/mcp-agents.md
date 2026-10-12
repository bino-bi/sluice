<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# MCP agents

An AI agent queries Sluice over MCP. Policies apply to MCP calls as they do to REST calls.

## Example

Start the server with `examples/mcp-agent/serve.sh`. MCP listens on port 8081.

```yaml
--8<-- "examples/mcp-agent/server.yaml:mcp"
```

The agent's API key maps to the group `agents`. The group may read `products`, and every query gets a row limit.

```yaml
--8<-- "examples/mcp-agent/policies.d/allow-agents.yaml:2"
```

```yaml
--8<-- "examples/mcp-agent/policies.d/limit-agents.yaml:2"
```

Open a session. The step stores the session id in `data/mcp-session`.

```bash
--8<-- "examples/mcp-agent/steps/01-initialize.sh"
```

Read the columns of `products`. The table list is empty until `describe_table` has read the catalog.

```bash
--8<-- "examples/mcp-agent/steps/03-describe-table.sh"
```

List the tables that the agent may read.

```bash
--8<-- "examples/mcp-agent/steps/04-list-tables.sh"
```

```json
--8<-- "examples/mcp-agent/expected/04-list-tables.txt"
```

Run a query.

```bash
--8<-- "examples/mcp-agent/steps/05-query.sh"
```

```json
--8<-- "examples/mcp-agent/expected/05-query.txt"
```

**What happened:** Sluice checked the key on each request and applied the policies of the group. The query keeps its `LIMIT 2` because it is below the limit.

## More examples

A query without `LIMIT` returns three of the five rows, and `truncated` is `true`.

```bash
--8<-- "examples/mcp-agent/steps/06-query-limit.sh"
```

```json
--8<-- "examples/mcp-agent/expected/06-query-limit.txt"
```

A query on `customers` is denied. The agent gets `ACL_DENIED` as a tool error.

```bash
--8<-- "examples/mcp-agent/steps/07-query-denied.sh"
```

```json
--8<-- "examples/mcp-agent/expected/07-query-denied.txt"
```

### Claude Desktop

Claude Desktop starts `sluice mcp` over stdio with this config. `sluice mcp` checks the key once at start. Replace the path. Run `serve.sh` once to create the data, then stop it. The checks do not run this file.

```json
--8<-- "examples/mcp-agent/manual/claude-desktop.json"
```

## Limits

- Sluice writes an audit record for `execute_sql` and `explain_access`, not for the other tools.

## See also

- [MCP tools](../reference/mcp.md)
- [Guardrails](../policies/guardrails.md)
- [Subjects](../policies/subjects.md)
