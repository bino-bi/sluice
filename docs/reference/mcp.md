<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# MCP tools

Sluice exposes a Model Context Protocol server so AI agents can query your
data under the same policy engine as everything else. MCP requests go
through the **identical** pipeline as `POST /v1/query` — parse, policy
evaluation, rewrite, DuckDB execution, and audit (records carry
`origin: mcp`). There is no MCP-only bypass.

## Running the server

Two ways:

- **`sluice mcp`** — MCP only, stdio by default. Flags: `--config`,
  `--policies-dir`, `--transport stdio|streamable_http`, `--jwt <token>`
  (falls back to the `SLUICE_MCP_TOKEN` env var), `--api-key <id>.<secret>`,
  `--allow-anonymous`. See [CLI](cli.md#mcp).
- **Inside `sluice serve`** — set `mcp.enabled: true` in `server.yaml`, with
  `mcp.transport`, `mcp.listen` (Streamable HTTP bind address), and
  `mcp.sessionIdleMax`. The serve-embedded stdio transport pins its
  identity from `mcp.tokenRef` / `mcp.apiKeyRef` (`secret://` references,
  e.g. `secret://env/SLUICE_MCP_TOKEN`); without one, `serve` refuses to
  start unless `mcp.allowAnonymous: true` opts into a default-denied
  anonymous run. `sluice config validate` catches the misconfiguration at
  exit code 3.

## Identity model

| Transport | Authentication |
|---|---|
| `stdio` | One identity, resolved **once at startup** and pinned onto every tool call — from `--jwt` / `--api-key` / `SLUICE_MCP_TOKEN` for `sluice mcp`, or from `mcp.tokenRef` / `mcp.apiKeyRef` for the serve-embedded transport. No credential and no allow-anonymous opt-in (`--allow-anonymous` / `mcp.allowAnonymous`) → refuses to start. |
| `streamable_http` | **Every request** is authenticated from its `Authorization` / `X-Api-Key` header, fail-closed: an invalid credential is always rejected with `401`, and a missing one is rejected too unless allow-anonymous (`--allow-anonymous` / `mcp.allowAnonymous`) is set. A leaked `Mcp-Session-Id` alone grants nothing. |

Anonymous sessions still hit default-deny — queries fail unless a policy
explicitly allows the anonymous subject.

## Tools

### execute_sql

Input: `sql` (SELECT statement), `row_limit` (optional, 1..100000).
Result: `{query_id, columns[], rows[][], row_count, truncated}` plus a
rendered text table. Policy errors come back as tool errors carrying the
same codes as REST (for example `ACL_DENIED`, `ERR_APPROVAL_PENDING`).

### list_catalogs

Input: none. Result: `{catalogs: [{name, type, healthy}]}` — the attached
data sources.

### list_tables

Input: `catalog` (required), `schema` (optional).
Result: `{tables: ["catalog.schema.table", …]}`.

### describe_table

Input: `table` — fully qualified `catalog.schema.table` (two-part
`catalog.table` accepted). Result:
`{table, columns: [{name, type, nullable}]}`.

### whoami

Input: none. Result: `{anonymous, subject, issuer, email, groups,
auth_method}` — the identity this session is authenticated as.

### explain_access

Input: `table` (fully qualified) or `sql` (a candidate SELECT).
Result: `{subject, resource, decision, row_filters[], column_masks[],
matched[], rejected[]}` — which policies apply and why, **without running
the query**. Agents should call this before `execute_sql` to avoid burning
a query on a deny.

### list_accessible_tables

Input: `catalog` (optional restriction). Result: `{tables: […]}` — the
tables the current identity is allowed to query.

### check_approval

Input: `approval_id` (from an `ERR_APPROVAL_PENDING` error).
Result: `{approval_id, state, expires_at}` with state
`pending | approved | rejected | expired`. Once approved, re-run the
**identical** `execute_sql` to consume the grant.

### await_approval

Input: `approval_id`, `timeout_seconds` — blocks until the request is
decided or the timeout elapses, then returns the same shape as
`check_approval`. The timeout is capped at 55 seconds (values ≤ 0 or > 55
become 55); prefer this over polling `check_approval` in a loop.

## Claude Desktop configuration

Drop this into `~/Library/Application Support/Claude/claude_desktop_config.json`
(macOS) or your platform's equivalent. Claude Desktop does not resolve
relative paths, so use absolute ones:

```json
{
  "mcpServers": {
    "sluice": {
      "command": "/ABSOLUTE/PATH/TO/sluice/bin/sluice",
      "args": [
        "mcp",
        "--config=/ABSOLUTE/PATH/TO/server.yaml",
        "--policies-dir=/ABSOLUTE/PATH/TO/policies.d",
        "--api-key=agent.YOUR-KEY-MATERIAL"
      ],
      "env": {
        "SLUICE_APIKEY_PEPPER": "your-pepper-value"
      }
    }
  }
}
```

The `env` block feeds the `secret://env/…` references in your `server.yaml`
(such as `identity.apiKeyPepper`). A runnable variant — using
`sluice serve` with `mcp.enabled: true` — ships in
`examples/mcp-agent/claude-desktop.json`.

For an end-to-end agent walkthrough (policies, approval loop, prompts), see
[MCP agents](../cookbook/mcp-agents.md).
