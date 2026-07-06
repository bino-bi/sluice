<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# mcp-agent

Expose Sluice as an MCP server to a local agent (Claude Desktop,
@modelcontextprotocol/inspector, or any SDK client). The agent sees
nine tools — `execute_sql`, `list_catalogs`, `list_tables`,
`describe_table`, `whoami`, `explain_access`,
`list_accessible_tables`, `check_approval`, `await_approval` — and
every call runs through the same parse → policy → rewrite → audit
pipeline as the REST transport.

## What this demonstrates

- **MCP over stdio.** The agent spawns `sluice serve` as a child
  process and talks JSON-RPC over stdin/stdout. No network exposure.
- **Policy is the safety rail.** The agent can SELECT from
  `shop.main.products` — and nothing else. An agent that tries to
  list `pg_stat_activity` gets an `ACL_DENIED` response, visible to
  the LLM so it can self-correct without a protocol-level crash.
- **Describe-first discovery.** `describe_table` returns column names
  and types without leaking a single row. Agents should call this
  before issuing real queries — the tool schema nudges them to do so.

## Layout

```
mcp-agent/
├── README.md
├── claude-desktop.json       — drop into Claude Desktop's config
├── server.yaml               — mcp.enabled = true, transport: stdio
├── seed.sql                  — 5 products, no PII
├── policies.d/
│   ├── datasource-catalog.yaml
│   ├── binding-apikey.yaml
│   └── allow-agents.yaml     — products only
└── data/                     — shop.db + audit log (runtime)
```

## Prepare the catalog

Run everything below from this directory (`examples/mcp-agent`) —
`server.yaml` and the datasource use relative paths (`./data/shop.db`,
`./data/audit`) that resolve against the process working directory.

```bash
sqlite3 data/shop.db < seed.sql
```

## Use with @modelcontextprotocol/inspector

```bash
# from examples/mcp-agent, after `make build` at the repo root
npx @modelcontextprotocol/inspector \
  ../../bin/sluice serve \
  --config server.yaml \
  --policies-dir policies.d
```

The inspector UI lists nine tools. Try in order:

1. `list_catalogs` — returns `[{name: "shop", ...}]`.
2. `list_tables { catalog: "shop" }` — returns
   `{"tables": ["shop.main.products"]}` (names are fully qualified).
3. `describe_table { table: "shop.main.products" }`
   — returns columns + types.
4. `execute_sql { sql: "SELECT name, price_cents FROM shop.main.products ORDER BY price_cents DESC" }`
   — returns rows.

## Use with Claude Desktop

Edit `claude-desktop.json`, replace every
`/ABSOLUTE/PATH/TO/sluice` with the checkout path, then copy the
file into `~/Library/Application Support/Claude/claude_desktop_config.json`
(macOS) or the equivalent for your platform. Restart Claude Desktop —
an `MCP Servers > sluice` entry appears in the settings panel.

## Verify the audit trail

```bash
# still from examples/mcp-agent
../../bin/sluice audit verify data/audit
# chain OK (1 file(s), N record(s), last_hash=...)
```

Every MCP tool call produces an audit record with `origin: "mcp"` and
the calling subject, so a post-hoc review of "what did the agent do"
is the same jq exercise as for any other transport.

## Not for production

- Stdio MCP runs with whatever privileges the parent process holds.
  Real deployments should either (a) pin the agent to a read-only
  filesystem with a scoped DuckDB catalog, or (b) use the
  Streamable HTTP transport behind a proxy that applies real
  authentication — never expose stdio to an untrusted network.
- The allowed table list should mirror your LLM's intended task
  surface. A broader policy makes the agent more capable; it also
  widens the blast radius of prompt injection. Start narrow.
