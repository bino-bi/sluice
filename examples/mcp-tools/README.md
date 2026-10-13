<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# mcp-tools

MCP over HTTP: the tool names, and which tables `list_tables` shows.

## Run

```bash
examples/mcp-tools/serve.sh
```

The script creates `data/shop.db` from `seed.sql`. REST listens on `http://localhost:8080`, MCP on `http://localhost:8081`. Then run the steps in order from this folder. The first step opens an MCP session and stores its id in `data/mcp-session`.

| Step | Shows |
|---|---|
| `steps/01-initialize.sh` | Opens the session. |
| `steps/02-tool-names.sh` | The names of all tools. |
| `steps/03-list-tables.sh` | `list_tables` shows both tables that the policy allows, not `customers`. |
| `steps/04-query.sh` | A query on `products`. |
| `steps/05-describe-table.sh` | The columns of `products`. |

`expected/` holds the real output of each step. See [the examples guide](../README.md) for the layout.
