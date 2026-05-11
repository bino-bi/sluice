<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# MCP tools

Sluice exposes a Model Context Protocol server — versioned against the
`2025-11-25` MCP spec — for LLM agents. The server supports **stdio**
(local agent, one process per session) and **Streamable HTTP** (hosted
agent platform).

## Tools

| Tool             | Purpose                                                   |
| ---------------- | --------------------------------------------------------- |
| `execute_sql`    | Same pipeline as `POST /v1/query` — parse, policy, rewrite, execute, audit. |
| `list_catalogs`  | Enumerate attached catalogs and their healthy state.       |
| `list_tables`    | Schemas + tables for a named catalog.                     |
| `describe_table` | Columns + types for a fully qualified table.              |

## Error handling

Tool errors are returned as `CallToolResult{ IsError: true, Content: [...] }`
so the LLM sees them as normal tool output. Protocol-level errors are
reserved for transport failures — never for policy denials, so the
model can self-correct without the session dying.

## Identity

- **stdio** inherits the parent process's environment; the future
  `sluice mcp` subcommand synthesises a `UserCtx` at startup.
- **Streamable HTTP** uses the same `identity.Composite` middleware as
  REST. A resumed session re-validates the token against the issuer's
  JWKS before any tool call proceeds.

## Try it locally

```bash
npx @modelcontextprotocol/inspector stdio ./bin/sluice mcp
# 4 tools listed: execute_sql, list_catalogs, list_tables, describe_table
```
