<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# mcp-tools

MCP over HTTP: the tool names, and when `list_tables` shows a table.

## Run

```bash
examples/mcp-tools/serve.sh
```

The script creates `data/shop.db` from `seed.sql`. REST listens on `http://localhost:8080`, MCP on `http://localhost:8081`. Then run the steps in order from this folder. The first step opens an MCP session and stores its id in `data/mcp-session`.

| Step | Shows |
|---|---|
| `steps/01-initialize.sh` | Opens the session. |
| `steps/02-tool-names.sh` | The names of all tools. |
| `steps/03-list-tables.sh` | A fresh server lists no tables. |
| `steps/04-describe-table.sh` | The columns of `products`. This loads the schema of the table. |
| `steps/05-list-tables-again.sh` | Now `list_tables` shows `products`. |

`expected/` holds the real output of each step. See [the examples guide](../README.md) for the layout.
