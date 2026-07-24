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
data sources. Not paginated: catalog counts are operator-bounded and
small.

### list_tables

Input: `catalog` (required), `schema` (optional), `limit` (optional, max
tables per page — default 500, max 1000), `cursor` (optional, opaque
cursor from a previous `next_cursor`).
Result: `{tables: ["catalog.schema.table", …], next_cursor?}`. Tables
come in stable lexicographic order; `next_cursor` is present while more
pages exist — pass it back as `cursor` to continue.

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

Input: `catalog` (optional restriction), `limit` / `cursor` (same paging
contract as `list_tables`). Result: `{tables: […], next_cursor?}` — the
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

## Visibility semantics

The metadata tools filter every candidate table through the policy
engine, with three deliberate nuances:

- **Only an explicit Deny hides a table.** Tables behind a
  `QueryReject` shape rule or an approval gate still list: they are
  legitimately reachable (a conforming or approved query succeeds), and
  hiding them would make the approval flow undiscoverable.
- **`describe_table` returns every column, including mask targets.**
  Column names and types are the metadata an agent needs to write a
  valid query; the *values* of masked columns are masked at execution.
- **Catalogs with no known tables stay visible** in `list_catalogs`.
  The operator attached them; without table metadata there is no basis
  to prove they are off-limits, and hiding them would strand
  legitimately empty catalogs.

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
