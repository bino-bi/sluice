<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# MCP agents

Goal: an LLM agent (Claude, ChatGPT, etc.) queries your warehouse
through Sluice. The same policies that govern humans govern the bot.

## Stdio agent (local, single user)

Most agent harnesses know how to spawn an MCP server over stdio.
Configure the agent with:

```json
{
  "name": "sluice",
  "command": "/usr/local/bin/sluice",
  "args": ["mcp", "--policies-dir", "/etc/sluice/policies.d",
                  "--config",       "/etc/sluice/server.yaml"],
  "env": {
    "SLUICE_APIKEY_PEPPER": "${env:SLUICE_APIKEY_PEPPER}",
    "SLUICE_AUDIT_GENESIS": "${env:SLUICE_AUDIT_GENESIS}"
  }
}
```

The agent sees four tools: `execute_sql`, `list_catalogs`,
`list_tables`, `describe_table`. Errors come back as tool output
(`IsError: true`) so the model can self-correct — "your SELECT on
`customers.email` was denied; email is masked for this session" is a
valid response the agent will handle.

## Streamable HTTP (hosted agent platforms)

When running a hosted agent (Claude.ai tool, Anthropic's built-in MCP
server on claude.ai/projects, ChatGPT custom connector), configure
Sluice with:

```yaml
mcp:
  enabled: true
  transport: http
  listen: ':8081'
```

Each session resolves its JWT through the same `identity.Composite`
as the REST endpoint. Session resume re-validates the token against
the issuer's JWKS before any tool call proceeds.

## Prompt engineering hints

- The `describe_table` tool returns column names + types. Agents that
  plan before querying use this effectively; agents that just try SQL
  tend to get blocked by the syntax-on-protected-table error.
- Include the policy surface in the system prompt: "Some columns
  (email, phone) may be masked; the response will be NULL or a
  constant — do not retry." This avoids the "let me try again
  differently" loop.
