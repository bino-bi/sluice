<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# mcp-agent

An AI agent queries a shop catalog through Sluice over MCP. It may read `products`, not `customers`. A row limit applies to every query.

## Run

```bash
examples/mcp-agent/serve.sh
```

REST listens on `http://localhost:8080`, MCP on `http://localhost:8081`. Then run the commands in `steps/` in order, from this folder. The first step opens an MCP session and stores its id in `data/mcp-session`.

The policy tests need no server:

```bash
sluice policy test policies.d --tests tests
```

## Claude Desktop

`manual/claude-desktop.json` starts `sluice mcp` over stdio. Replace `/ABSOLUTE/PATH/TO/sluice` with your checkout path. Run `serve.sh` once to create the data, then stop it. Two Sluice processes must not write to the same `data/audit`.

## Files

| File | Content |
|---|---|
| `server.yaml` | Server config with MCP over HTTP. |
| `seed.sql` | Tables `products` and `customers`. |
| `policies.d/` | Data source, agent identity, allow policy, row limit. |
| `tests/agent.yaml` | Allow, deny and the rewritten SQL. |
| `steps/`, `expected/` | MCP calls with `curl` and their real output. |
| `manual/claude-desktop.json` | Not run by the check. |

See [the examples guide](../README.md) for the layout.
